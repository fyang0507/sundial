package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fyang0507/sundial/internal/model"
	"gopkg.in/yaml.v3"
)

// ConfigFilename is the name of the single sundial config file that lives in
// the sundial source repo. The daemon reads it at startup via the --config
// flag or the SUNDIAL_CONFIG env var; the fallback search looks for it in cwd
// and next to the running binary.
const ConfigFilename = "sundial.config.yaml"

// decodeConfigFile reads the YAML file at path and strictly decodes it into a
// model.Config. KnownFields(true) makes a dotted key (e.g.
// "daemon.wake.enabled: true"), a typo, or a misplaced field a hard error
// naming the offending field rather than a silent ignore.
func decodeConfigFile(path string) (*model.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	var cfg model.Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	return &cfg, nil
}

// LoadConfigFile reads the single config file at configPath, strictly decodes
// it into a model.Config, applies defaults for zero-value fields, and expands
// ~ in all path fields. A relative data_repo_path is resolved from the config
// file's directory. If dataRepoOverride is non-empty it overrides the on-disk
// data_repo_path. The resolved absolute config path is recorded on Config.ConfigPath.
func LoadConfigFile(configPath, dataRepoOverride string) (*model.Config, error) {
	cfg, err := decodeConfigFile(configPath)
	if err != nil {
		return nil, err
	}

	if dataRepoOverride != "" {
		cfg.DataRepo = dataRepoOverride
	}

	if abs, err := filepath.Abs(configPath); err == nil {
		cfg.ConfigPath = abs
	} else {
		cfg.ConfigPath = configPath
	}

	applyDefaults(cfg)
	if dataRepoOverride == "" {
		cfg.DataRepo = resolveConfigRelativePath(cfg.ConfigPath, cfg.DataRepo)
	}
	expandPaths(cfg)
	return cfg, nil
}

// LoadAndResolve locates the single config file, loads it, and applies the
// data-repo override precedence. The config file is located by:
//
//  1. configFlag (the --config flag), if non-empty
//  2. SUNDIAL_CONFIG env var
//  3. ./sundial.config.yaml in the current working directory
//  4. sundial.config.yaml next to the running binary
//
// data_repo_path comes from the loaded file but is overridden, in order, by a
// non-empty dataRepoFlag (--data-repo) then SUNDIAL_DATA_REPO. It returns the
// populated Config and the resolved config-file path.
func LoadAndResolve(configFlag, dataRepoFlag string) (*model.Config, string, error) {
	configPath, err := resolveConfigPath(configFlag)
	if err != nil {
		return nil, "", err
	}

	cfg, err := LoadConfigFile(configPath, "")
	if err != nil {
		return nil, configPath, err
	}

	// data_repo_path overrides: --data-repo flag, then SUNDIAL_DATA_REPO env.
	if dataRepoFlag != "" {
		cfg.DataRepo = ExpandPath(dataRepoFlag)
	} else if env := os.Getenv("SUNDIAL_DATA_REPO"); env != "" {
		cfg.DataRepo = ExpandPath(env)
	}

	return cfg, cfg.ConfigPath, nil
}

// resolveConfigPath finds the config file following the documented order:
// --config flag → SUNDIAL_CONFIG env → ./sundial.config.yaml → next to binary.
// Returns model.ErrConfigNotResolved with remediation guidance otherwise.
func resolveConfigPath(configFlag string) (string, error) {
	if configFlag != "" {
		return ExpandPath(configFlag), nil
	}
	if env := os.Getenv("SUNDIAL_CONFIG"); env != "" {
		return ExpandPath(env), nil
	}

	// ./sundial.config.yaml in the current working directory.
	if cwd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(cwd, ConfigFilename)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	// sundial.config.yaml next to the running binary.
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), ConfigFilename)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf(
		"could not locate %s: pass --config <path>, set SUNDIAL_CONFIG, or place %s in the working directory: %w",
		ConfigFilename, ConfigFilename, model.ErrConfigNotResolved,
	)
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
	if len(cfg.Daemon.PreconditionBackoff) == 0 {
		cfg.Daemon.PreconditionBackoff = model.DefaultPreconditionBackoff
	}
	if cfg.Daemon.PreconditionMaxElapsed == "" {
		cfg.Daemon.PreconditionMaxElapsed = model.DefaultPreconditionMaxElapsed
	}
	if cfg.Daemon.MissGracePeriod == "" {
		cfg.Daemon.MissGracePeriod = model.DefaultMissGracePeriod
	}
	// Wake.Enabled defaults to false naturally (the zero value); only the lead
	// time needs a default so an enabled-but-unspecified config still wakes early.
	if cfg.Daemon.Wake.LeadTime == "" {
		cfg.Daemon.Wake.LeadTime = model.DefaultWakeLeadTime
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

// resolveConfigRelativePath expands ~, then anchors a relative value at the
// config file's directory so the config remains portable across mount points.
func resolveConfigRelativePath(configPath, p string) string {
	expanded := ExpandPath(p)
	if expanded == "" || filepath.IsAbs(expanded) {
		return expanded
	}
	configAbs, err := filepath.Abs(configPath)
	if err != nil {
		return expanded
	}
	return filepath.Clean(filepath.Join(filepath.Dir(configAbs), expanded))
}

// Validate checks that cfg satisfies all invariants:
//   - DataRepo is non-empty (data_repo_path was set in the config file or overridden)
//   - DataRepo path exists on disk
//   - DataRepo contains a .git directory
//   - LogLevel (if set) is one of: debug, info, warn, error
func Validate(cfg *model.Config) error {
	if cfg.DataRepo == "" {
		return fmt.Errorf("data_repo_path is required: %w", model.ErrConfigInvalid)
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
