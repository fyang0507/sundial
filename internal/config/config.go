package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fyang0507/sundial/internal/model"
	"gopkg.in/yaml.v3"
)

// SundialConfigRel is the data-repo-relative path to sundial's daemon config.
const SundialConfigRel = "sundial/config.yaml"

// ConfigPath returns the resolved config file path for a given data repo.
func ConfigPath(dataRepo string) string {
	return filepath.Join(dataRepo, SundialConfigRel)
}

// Load reads a YAML config file at path, unmarshals it into model.Config,
// applies defaults for zero-value fields, and expands ~ in all path fields.
// Does not populate DataRepo — callers inject that from the resolver.
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

// LoadAndResolve resolves the data repo (env/dev/walk-up), loads the daemon
// config from <data_repo>/sundial/config.yaml (defaults applied if absent),
// injects DataRepo, and returns the populated Config along with the resolved
// config-file path (empty string if no file was present).
func LoadAndResolve() (*model.Config, string, error) {
	res, err := ResolveDataRepo()
	if err != nil {
		return nil, "", err
	}
	return loadForDataRepo(res.DataRepo)
}

// LoadForDataRepo is LoadAndResolve but with an explicit data repo path,
// bypassing resolution. Used by `sundial setup --data-repo`.
func LoadForDataRepo(dataRepo string) (*model.Config, string, error) {
	return loadForDataRepo(ExpandPath(dataRepo))
}

func loadForDataRepo(dataRepo string) (*model.Config, string, error) {
	cfgPath := ConfigPath(dataRepo)
	cfg := &model.Config{}

	if data, err := os.ReadFile(cfgPath); err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, cfgPath, fmt.Errorf("parsing %s: %w", cfgPath, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, cfgPath, fmt.Errorf("reading %s: %w", cfgPath, err)
	} else {
		cfgPath = "" // absent, not an error — defaults fill in
	}

	cfg.DataRepo = dataRepo
	applyDefaults(cfg)
	expandPaths(cfg)
	return cfg, cfgPath, nil
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
		return fmt.Errorf("data_repo is required: %w", model.ErrConfigInvalid)
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
