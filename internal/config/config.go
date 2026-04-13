package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fyang0507/sundial/internal/model"
	"gopkg.in/yaml.v3"
)

// Load reads a YAML config file at path, unmarshals it into model.Config,
// applies defaults for zero-value fields, and expands ~ in all path fields.
func Load(path string) (*model.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg model.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	applyDefaults(&cfg)
	expandPaths(&cfg)

	return &cfg, nil
}

// applyDefaults fills in default values from model.Default* constants
// for any zero-value fields.
func applyDefaults(cfg *model.Config) {
	if cfg.Daemon.SocketPath == "" {
		cfg.Daemon.SocketPath = model.DefaultSocketPath
	}
	if cfg.Daemon.LogLevel == "" {
		cfg.Daemon.LogLevel = model.DefaultLogLevel
	}
	if cfg.Daemon.LogFile == "" {
		cfg.Daemon.LogFile = model.DefaultLogFile
	}
	if cfg.State.Path == "" {
		cfg.State.Path = model.DefaultStatePath
	}
	if cfg.State.LogsPath == "" {
		cfg.State.LogsPath = model.DefaultLogsPath
	}
}

// expandPaths expands ~ to the user's home directory in all path fields.
func expandPaths(cfg *model.Config) {
	cfg.DataRepo = ExpandPath(cfg.DataRepo)
	cfg.Daemon.SocketPath = ExpandPath(cfg.Daemon.SocketPath)
	cfg.Daemon.LogFile = ExpandPath(cfg.Daemon.LogFile)
	cfg.State.Path = ExpandPath(cfg.State.Path)
	cfg.State.LogsPath = ExpandPath(cfg.State.LogsPath)
}

// ExpandPath replaces a leading ~/ with the user's home directory.
// If p does not start with ~, it is returned unchanged.
func ExpandPath(p string) string {
	if p == "" {
		return p
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return p
		}
		return filepath.Join(home, p[1:])
	}
	return p
}

// Validate checks that cfg satisfies all invariants:
//   - DataRepo is non-empty
//   - DataRepo path exists on disk
//   - DataRepo contains a .git directory
//   - LogLevel (if set) is one of: debug, info, warn, error
func Validate(cfg *model.Config) error {
	if cfg.DataRepo == "" {
		return fmt.Errorf("data_repo is required in config.yaml: %w", model.ErrConfigInvalid)
	}

	info, err := os.Stat(cfg.DataRepo)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("data_repo path invalid: %s: %w", cfg.DataRepo, model.ErrDataRepoInvalid)
	}

	gitDir := filepath.Join(cfg.DataRepo, ".git")
	if info, err := os.Stat(gitDir); err != nil || !info.IsDir() {
		return fmt.Errorf("data_repo is not a git repository: %s: %w", cfg.DataRepo, model.ErrDataRepoInvalid)
	}

	validLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if cfg.Daemon.LogLevel != "" && !validLevels[cfg.Daemon.LogLevel] {
		return fmt.Errorf("invalid log_level %q: must be one of debug, info, warn, error: %w",
			cfg.Daemon.LogLevel, model.ErrConfigInvalid)
	}

	return nil
}

// FindConfigPath locates a config.yaml file by:
//  1. Checking the SUNDIAL_CONFIG environment variable
//  2. Looking in the directory of the running executable
//
// Returns model.ErrConfigNotFound if neither location has a config file.
func FindConfigPath() (string, error) {
	// Check env var first.
	if envPath := os.Getenv("SUNDIAL_CONFIG"); envPath != "" {
		envPath = ExpandPath(envPath)
		if _, err := os.Stat(envPath); err == nil {
			return envPath, nil
		}
	}

	// Check directory of the running executable.
	exe, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exe)
		candidate := filepath.Join(exeDir, "config.yaml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", model.ErrConfigNotFound
}
