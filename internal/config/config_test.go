package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fyang0507/sundial/internal/model"
)

// writeConfig writes a YAML string to path/config.yaml and returns the full path.
func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "config.yaml")
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

func TestLoad_AllFieldsSet(t *testing.T) {
	tmp := t.TempDir()

	yaml := `daemon:
  socket_path: /tmp/test.sock
  log_level: debug
  log_file: /tmp/test.log
state:
  path: /tmp/state/
  logs_path: /tmp/logs/
`
	cfgPath := writeConfig(t, tmp, yaml)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Daemon.SocketPath != "/tmp/test.sock" {
		t.Errorf("SocketPath = %q, want /tmp/test.sock", cfg.Daemon.SocketPath)
	}
	if cfg.Daemon.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", cfg.Daemon.LogLevel)
	}
	if cfg.Daemon.LogFile != "/tmp/test.log" {
		t.Errorf("LogFile = %q, want /tmp/test.log", cfg.Daemon.LogFile)
	}
	if cfg.State.Path != "/tmp/state/" {
		t.Errorf("State.Path = %q, want /tmp/state/", cfg.State.Path)
	}
	if cfg.State.LogsPath != "/tmp/logs/" {
		t.Errorf("State.LogsPath = %q, want /tmp/logs/", cfg.State.LogsPath)
	}
}

func TestLoad_MinimalConfig_DefaultsApplied(t *testing.T) {
	tmp := t.TempDir()

	cfgPath := writeConfig(t, tmp, "")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Defaults should be applied and ~ expanded.
	home, _ := os.UserHomeDir()

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
	// Directory exists but has no .git
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
		{
			name: "tilde prefix",
			in:   "~/foo",
			want: filepath.Join(home, "foo"),
		},
		{
			name: "absolute path unchanged",
			in:   "/absolute/path",
			want: "/absolute/path",
		},
		{
			name: "empty string",
			in:   "",
			want: "",
		},
		{
			name: "bare tilde",
			in:   "~",
			want: home,
		},
		{
			name: "no tilde prefix",
			in:   "relative/path",
			want: "relative/path",
		},
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

func TestResolveDataRepo_EnvVar(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SUNDIAL_DATA_REPO", tmp)

	got, err := ResolveDataRepo()
	if err != nil {
		t.Fatalf("ResolveDataRepo() error: %v", err)
	}
	if got.DataRepo != tmp {
		t.Errorf("DataRepo = %q, want %q", got.DataRepo, tmp)
	}
	if got.Source != ResolveSourceEnv {
		t.Errorf("Source = %q, want %q", got.Source, ResolveSourceEnv)
	}
}

func TestResolveDataRepo_WorkspaceWalkUp(t *testing.T) {
	t.Setenv("SUNDIAL_DATA_REPO", "")

	// Create a fake data repo with .agents/workspace.yaml.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".agents"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".agents/workspace.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	// cd into a subdirectory of the data repo.
	sub := filepath.Join(root, "sub", "deeper")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	prev, _ := os.Getwd()
	defer os.Chdir(prev)
	if err := os.Chdir(sub); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	got, err := ResolveDataRepo()
	if err != nil {
		t.Fatalf("ResolveDataRepo() error: %v", err)
	}
	// Resolve symlinks (macOS temp dirs are usually under /private/var/...).
	wantReal, _ := filepath.EvalSymlinks(root)
	gotReal, _ := filepath.EvalSymlinks(got.DataRepo)
	if gotReal != wantReal {
		t.Errorf("DataRepo = %q, want %q", gotReal, wantReal)
	}
	if got.Source != ResolveSourceWorkspace {
		t.Errorf("Source = %q, want %q", got.Source, ResolveSourceWorkspace)
	}
}

func TestResolveDataRepo_NotResolved(t *testing.T) {
	t.Setenv("SUNDIAL_DATA_REPO", "")

	// cd into a temp dir that has no .agents/workspace.yaml anywhere up the chain.
	// On macOS, `os.TempDir()` is under /var/folders/...; we can't guarantee the
	// whole chain is marker-free. Instead, move cwd to $TMPDIR itself and hope.
	// This test is best-effort; skip if a marker is detected higher up.
	dir := t.TempDir()
	prev, _ := os.Getwd()
	defer os.Chdir(prev)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if _, ok := walkUpForWorkspace(); ok {
		t.Skip("environment has a workspace.yaml above the temp dir; skipping")
	}

	_, err := ResolveDataRepo()
	if !errors.Is(err, model.ErrDataRepoNotResolved) {
		t.Errorf("ResolveDataRepo() error = %v, want ErrDataRepoNotResolved", err)
	}
}

func TestLoadForDataRepo_MissingFileUsesDefaults(t *testing.T) {
	tmp := t.TempDir()
	repo := makeGitRepo(t, tmp)

	cfg, cfgPath, err := LoadForDataRepo(repo)
	if err != nil {
		t.Fatalf("LoadForDataRepo() error: %v", err)
	}
	if cfg.DataRepo != repo {
		t.Errorf("DataRepo = %q, want %q", cfg.DataRepo, repo)
	}
	if cfgPath != "" {
		t.Errorf("cfgPath = %q, want empty (file absent)", cfgPath)
	}
	if cfg.Daemon.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info (default)", cfg.Daemon.LogLevel)
	}
}

func TestLoadForDataRepo_WithFileOverridesDefaults(t *testing.T) {
	tmp := t.TempDir()
	repo := makeGitRepo(t, tmp)

	cfgDir := filepath.Join(repo, "sundial")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "daemon:\n  log_level: debug\n"
	cfgPath := filepath.Join(cfgDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, got, err := LoadForDataRepo(repo)
	if err != nil {
		t.Fatalf("LoadForDataRepo() error: %v", err)
	}
	if cfg.Daemon.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", cfg.Daemon.LogLevel)
	}
	if got != cfgPath {
		t.Errorf("resolved path = %q, want %q", got, cfgPath)
	}
}
