package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fyang0507/sundial/internal/model"
)

// writeConfig writes a YAML string to dir/sundial.config.yaml and returns the path.
func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, ConfigFilename)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return p
}

// makeGitRepo creates a directory with a .git subdirectory, simulating a repo.
func makeGitRepo(t *testing.T, dir string) string {
	t.Helper()
	repo := filepath.Join(dir, "data-repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("creating git repo: %v", err)
	}
	return repo
}

func TestLoadConfigFile_AllFieldsSet(t *testing.T) {
	tmp := t.TempDir()

	yaml := `data_repo_path: /tmp/data-repo
daemon:
  log_level: debug
  log_file: /tmp/test.log
  wake:
    enabled: true
    lead_time: 5m
state:
  path: /tmp/state/
  logs_path: /tmp/logs/
`
	cfgPath := writeConfig(t, tmp, yaml)

	cfg, err := LoadConfigFile(cfgPath, "")
	if err != nil {
		t.Fatalf("LoadConfigFile() error: %v", err)
	}

	if cfg.DataRepo != "/tmp/data-repo" {
		t.Errorf("DataRepo = %q, want /tmp/data-repo", cfg.DataRepo)
	}
	// ConfigPath is the absolute path of the file we loaded.
	wantAbs, _ := filepath.Abs(cfgPath)
	if cfg.ConfigPath != wantAbs {
		t.Errorf("ConfigPath = %q, want %q", cfg.ConfigPath, wantAbs)
	}
	// socket_path is not a user-configurable field; the expanded default always applies.
	home, _ := os.UserHomeDir()
	wantSocket := filepath.Join(home, "Library/Application Support/sundial/sundial.sock")
	if cfg.Daemon.SocketPath != wantSocket {
		t.Errorf("SocketPath = %q, want default %q", cfg.Daemon.SocketPath, wantSocket)
	}
	if cfg.Daemon.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", cfg.Daemon.LogLevel)
	}
	if cfg.Daemon.LogFile != "/tmp/test.log" {
		t.Errorf("LogFile = %q, want /tmp/test.log", cfg.Daemon.LogFile)
	}
	if !cfg.Daemon.Wake.Enabled {
		t.Error("Wake.Enabled = false, want true")
	}
	if cfg.Daemon.Wake.LeadTime != "5m" {
		t.Errorf("Wake.LeadTime = %q, want 5m", cfg.Daemon.Wake.LeadTime)
	}
	if cfg.State.Path != "/tmp/state/" {
		t.Errorf("State.Path = %q, want /tmp/state/", cfg.State.Path)
	}
	if cfg.State.LogsPath != "/tmp/logs/" {
		t.Errorf("State.LogsPath = %q, want /tmp/logs/", cfg.State.LogsPath)
	}
}

func TestLoadConfigFile_MinimalConfig_DefaultsApplied(t *testing.T) {
	tmp := t.TempDir()

	cfgPath := writeConfig(t, tmp, "data_repo_path: ~/some-repo\n")

	cfg, err := LoadConfigFile(cfgPath, "")
	if err != nil {
		t.Fatalf("LoadConfigFile() error: %v", err)
	}

	home, _ := os.UserHomeDir()

	// data_repo_path is tilde-expanded.
	if cfg.DataRepo != filepath.Join(home, "some-repo") {
		t.Errorf("DataRepo = %q, want %q", cfg.DataRepo, filepath.Join(home, "some-repo"))
	}

	wantSocket := filepath.Join(home, "Library/Application Support/sundial/sundial.sock")
	if cfg.Daemon.SocketPath != wantSocket {
		t.Errorf("SocketPath = %q, want %q", cfg.Daemon.SocketPath, wantSocket)
	}
	if cfg.Daemon.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", cfg.Daemon.LogLevel)
	}
	wantLogFile := filepath.Join(home, "Library/Logs/sundial/sundial.log")
	if cfg.Daemon.LogFile != wantLogFile {
		t.Errorf("LogFile = %q, want %q", cfg.Daemon.LogFile, wantLogFile)
	}
	wantState := filepath.Join(home, ".config/sundial/state")
	if cfg.State.Path != wantState {
		t.Errorf("State.Path = %q, want %q", cfg.State.Path, wantState)
	}
	wantLogs := filepath.Join(home, ".config/sundial/logs")
	if cfg.State.LogsPath != wantLogs {
		t.Errorf("State.LogsPath = %q, want %q", cfg.State.LogsPath, wantLogs)
	}
	wantBackoff := []string{"1m", "5m", "30m", "1h", "2h"}
	if len(cfg.Daemon.PreconditionBackoff) != len(wantBackoff) {
		t.Fatalf("PreconditionBackoff = %v, want %v", cfg.Daemon.PreconditionBackoff, wantBackoff)
	}
	for i, v := range wantBackoff {
		if cfg.Daemon.PreconditionBackoff[i] != v {
			t.Errorf("PreconditionBackoff[%d] = %q, want %q", i, cfg.Daemon.PreconditionBackoff[i], v)
		}
	}
	if cfg.Daemon.PreconditionMaxElapsed != "2h" {
		t.Errorf("PreconditionMaxElapsed = %q, want 2h", cfg.Daemon.PreconditionMaxElapsed)
	}
	if cfg.Daemon.MissGracePeriod != model.DefaultMissGracePeriod {
		t.Errorf("MissGracePeriod = %q, want %q", cfg.Daemon.MissGracePeriod, model.DefaultMissGracePeriod)
	}
	// Wake management is off by default; lead_time defaults to "3m".
	if cfg.Daemon.Wake.Enabled {
		t.Error("Wake.Enabled = true, want false by default")
	}
	if cfg.Daemon.Wake.LeadTime != model.DefaultWakeLeadTime {
		t.Errorf("Wake.LeadTime = %q, want %q", cfg.Daemon.Wake.LeadTime, model.DefaultWakeLeadTime)
	}
}

func TestRelativeDataRepoPathIsConfigRelative(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, "sundial")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("creating config directory: %v", err)
	}
	cfgPath := writeConfig(t, configDir, "data_repo_path: ../fred-agent\n")
	want := filepath.Join(tmp, "fred-agent")

	cfg, err := LoadConfigFile(cfgPath, "")
	if err != nil {
		t.Fatalf("LoadConfigFile() error: %v", err)
	}
	if cfg.DataRepo != want {
		t.Errorf("DataRepo = %q, want %q", cfg.DataRepo, want)
	}

	got, err := ReadDataRepoPath(cfgPath)
	if err != nil {
		t.Fatalf("ReadDataRepoPath() error: %v", err)
	}
	if got != want {
		t.Errorf("ReadDataRepoPath() = %q, want %q", got, want)
	}
}

func TestLoadConfigFile_DataRepoOverride(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := writeConfig(t, tmp, "data_repo_path: /from/file\n")

	cfg, err := LoadConfigFile(cfgPath, "/override/repo")
	if err != nil {
		t.Fatalf("LoadConfigFile() error: %v", err)
	}
	if cfg.DataRepo != "/override/repo" {
		t.Errorf("DataRepo = %q, want /override/repo (override wins)", cfg.DataRepo)
	}
}

// Strict decoding: an unknown top-level field is a hard error naming the field.
func TestLoadConfigFile_UnknownField(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := writeConfig(t, tmp, "data_repo_path: /x\nunknown_field: true\n")

	_, err := LoadConfigFile(cfgPath, "")
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "unknown_field") {
		t.Errorf("error %q does not name the offending field 'unknown_field'", err)
	}
}

// Strict decoding: a typo inside the daemon block is a hard error.
func TestLoadConfigFile_TypoInDaemonField(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := writeConfig(t, tmp, "data_repo_path: /x\ndaemon:\n  log_lvl: debug\n")

	_, err := LoadConfigFile(cfgPath, "")
	if err == nil {
		t.Fatal("expected error for typo'd daemon field, got nil")
	}
	if !strings.Contains(err.Error(), "log_lvl") {
		t.Errorf("error %q does not name the offending field 'log_lvl'", err)
	}
}

// Strict decoding: a dotted key (instead of nesting) is rejected.
func TestLoadConfigFile_DottedKeyRejected(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := writeConfig(t, tmp, "data_repo_path: /x\ndaemon.wake.enabled: true\n")

	_, err := LoadConfigFile(cfgPath, "")
	if err == nil {
		t.Fatal("expected error for dotted key, got nil")
	}
}

func TestLoadAndResolve_ConfigFlag(t *testing.T) {
	tmp := t.TempDir()
	repo := makeGitRepo(t, tmp)
	cfgPath := writeConfig(t, tmp, "data_repo_path: "+repo+"\ndaemon:\n  log_level: warn\n")

	cfg, gotPath, err := LoadAndResolve(cfgPath, "")
	if err != nil {
		t.Fatalf("LoadAndResolve() error: %v", err)
	}
	wantAbs, _ := filepath.Abs(cfgPath)
	if gotPath != wantAbs {
		t.Errorf("config path = %q, want %q", gotPath, wantAbs)
	}
	if cfg.DataRepo != repo {
		t.Errorf("DataRepo = %q, want %q", cfg.DataRepo, repo)
	}
	if cfg.Daemon.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want warn", cfg.Daemon.LogLevel)
	}
}

func TestLoadAndResolve_DataRepoFlagOverridesFile(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := writeConfig(t, tmp, "data_repo_path: /from/file\n")

	cfg, _, err := LoadAndResolve(cfgPath, "/flag/repo")
	if err != nil {
		t.Fatalf("LoadAndResolve() error: %v", err)
	}
	if cfg.DataRepo != "/flag/repo" {
		t.Errorf("DataRepo = %q, want /flag/repo (flag wins)", cfg.DataRepo)
	}
}

func TestLoadAndResolve_SundialDataRepoEnvOverridesFile(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := writeConfig(t, tmp, "data_repo_path: /from/file\n")
	t.Setenv("SUNDIAL_DATA_REPO", "/from/env")
	t.Setenv("SUNDIAL_CONFIG", "")

	cfg, _, err := LoadAndResolve(cfgPath, "")
	if err != nil {
		t.Fatalf("LoadAndResolve() error: %v", err)
	}
	if cfg.DataRepo != "/from/env" {
		t.Errorf("DataRepo = %q, want /from/env (env override)", cfg.DataRepo)
	}
}

func TestLoadAndResolve_SundialConfigEnv(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := writeConfig(t, tmp, "data_repo_path: /env/repo\n")
	t.Setenv("SUNDIAL_CONFIG", cfgPath)
	t.Setenv("SUNDIAL_DATA_REPO", "")

	cfg, gotPath, err := LoadAndResolve("", "")
	if err != nil {
		t.Fatalf("LoadAndResolve() error: %v", err)
	}
	wantAbs, _ := filepath.Abs(cfgPath)
	if gotPath != wantAbs {
		t.Errorf("config path = %q, want %q", gotPath, wantAbs)
	}
	if cfg.DataRepo != "/env/repo" {
		t.Errorf("DataRepo = %q, want /env/repo", cfg.DataRepo)
	}
}

func TestLoadAndResolve_CwdFallback(t *testing.T) {
	tmp := t.TempDir()
	writeConfig(t, tmp, "data_repo_path: /cwd/repo\n")
	t.Setenv("SUNDIAL_CONFIG", "")
	t.Setenv("SUNDIAL_DATA_REPO", "")

	prev, _ := os.Getwd()
	defer os.Chdir(prev)
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg, _, err := LoadAndResolve("", "")
	if err != nil {
		t.Fatalf("LoadAndResolve() error: %v", err)
	}
	if cfg.DataRepo != "/cwd/repo" {
		t.Errorf("DataRepo = %q, want /cwd/repo", cfg.DataRepo)
	}
}

func TestLoadAndResolve_NotResolved(t *testing.T) {
	t.Setenv("SUNDIAL_CONFIG", "")
	t.Setenv("SUNDIAL_DATA_REPO", "")

	// cd into an empty temp dir with no sundial.config.yaml.
	dir := t.TempDir()
	prev, _ := os.Getwd()
	defer os.Chdir(prev)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	_, _, err := LoadAndResolve("", "")
	if err == nil {
		t.Fatal("expected error when no config file can be located")
	}
	if !errors.Is(err, model.ErrConfigNotResolved) {
		t.Errorf("error = %v, want ErrConfigNotResolved", err)
	}
	if !IsResolveError(err) {
		t.Error("IsResolveError() = false, want true")
	}
}

func TestReadDataRepoPath(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := writeConfig(t, tmp, "data_repo_path: ~/repo-x\n")

	got, err := ReadDataRepoPath(cfgPath)
	if err != nil {
		t.Fatalf("ReadDataRepoPath() error: %v", err)
	}
	home, _ := os.UserHomeDir()
	if got != filepath.Join(home, "repo-x") {
		t.Errorf("ReadDataRepoPath() = %q, want %q", got, filepath.Join(home, "repo-x"))
	}
}

func TestReadDataRepoPath_Missing(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := writeConfig(t, tmp, "daemon:\n  log_level: info\n")

	_, err := ReadDataRepoPath(cfgPath)
	if err == nil {
		t.Fatal("expected error for config with no data_repo_path")
	}
}

func TestValidate_MissingDataRepo(t *testing.T) {
	cfg := &model.Config{}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for missing data_repo")
	}
	if !errors.Is(err, model.ErrConfigInvalid) {
		t.Errorf("error = %v, want wrapped ErrConfigInvalid", err)
	}
}

func TestValidate_NonexistentPath(t *testing.T) {
	cfg := &model.Config{DataRepo: "/nonexistent/path/that/does/not/exist"}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for nonexistent data_repo")
	}
	if !errors.Is(err, model.ErrDataRepoInvalid) {
		t.Errorf("error = %v, want wrapped ErrDataRepoInvalid", err)
	}
}

func TestValidate_PathExistsButNoGit(t *testing.T) {
	tmp := t.TempDir()
	cfg := &model.Config{DataRepo: tmp}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for missing .git directory")
	}
	if !errors.Is(err, model.ErrDataRepoInvalid) {
		t.Errorf("error = %v, want wrapped ErrDataRepoInvalid", err)
	}
}

func TestValidate_InvalidLogLevel(t *testing.T) {
	tmp := t.TempDir()
	repo := makeGitRepo(t, tmp)

	cfg := &model.Config{
		DataRepo: repo,
		Daemon:   model.DaemonConfig{LogLevel: "verbose"},
	}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for invalid log_level")
	}
	if !errors.Is(err, model.ErrConfigInvalid) {
		t.Errorf("error = %v, want wrapped ErrConfigInvalid", err)
	}
}

func TestValidate_ValidConfig(t *testing.T) {
	tmp := t.TempDir()
	repo := makeGitRepo(t, tmp)

	cfg := &model.Config{
		DataRepo: repo,
		Daemon:   model.DaemonConfig{LogLevel: "warn"},
	}
	if err := Validate(cfg); err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "tilde prefix", in: "~/foo", want: filepath.Join(home, "foo")},
		{name: "absolute path unchanged", in: "/absolute/path", want: "/absolute/path"},
		{name: "empty string", in: "", want: ""},
		{name: "bare tilde", in: "~", want: home},
		{name: "no tilde prefix", in: "relative/path", want: "relative/path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExpandPath(tt.in)
			if got != tt.want {
				t.Errorf("ExpandPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
