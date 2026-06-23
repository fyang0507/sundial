package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fyang0507/sundial/internal/config"
	"github.com/fyang0507/sundial/internal/daemon"
)

// writeConfigFile writes a sundial.config.yaml into dir and returns its path.
func writeConfigFile(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, config.ConfigFilename)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return p
}

// TestLoadAndResolve_SingleFileStartsDaemon proves the single config file model
// end to end: a real sundial.config.yaml carries data_repo_path + daemon/state
// blocks, config.LoadAndResolve loads it, Validate passes against the git data
// repo, and the daemon constructs from the resulting config.
func TestLoadAndResolve_SingleFileStartsDaemon(t *testing.T) {
	dataDir, err := os.MkdirTemp("", "sundial-cfg-data-*")
	if err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dataDir) })
	initGitRepo(t, dataDir)

	stateDir, err := os.MkdirTemp("", "sundial-cfg-state-*")
	if err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(stateDir) })
	logsDir, err := os.MkdirTemp("", "sundial-cfg-logs-*")
	if err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(logsDir) })

	socketPath := fmt.Sprintf("/tmp/sundial-cfg-%s.sock", randomHex(8))

	cfgDir := t.TempDir()
	body := fmt.Sprintf(`data_repo_path: %s
daemon:
  log_level: warn
state:
  path: %s
  logs_path: %s
`, dataDir, stateDir, logsDir)
	cfgPath := writeConfigFile(t, cfgDir, body)

	t.Setenv("SUNDIAL_CONFIG", "")
	t.Setenv("SUNDIAL_DATA_REPO", "")

	cfg, resolvedPath, err := config.LoadAndResolve(cfgPath, "")
	if err != nil {
		t.Fatalf("LoadAndResolve: %v", err)
	}
	wantAbs, _ := filepath.Abs(cfgPath)
	if resolvedPath != wantAbs {
		t.Errorf("resolved config path = %q, want %q", resolvedPath, wantAbs)
	}
	if cfg.DataRepo != dataDir {
		t.Errorf("DataRepo = %q, want %q", cfg.DataRepo, dataDir)
	}
	if cfg.ConfigPath != wantAbs {
		t.Errorf("cfg.ConfigPath = %q, want %q", cfg.ConfigPath, wantAbs)
	}
	if cfg.Daemon.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want warn", cfg.Daemon.LogLevel)
	}

	if err := config.Validate(cfg); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	// Use a test socket so we don't collide with the well-known path, and start
	// the daemon to prove the loaded config is usable.
	cfg.Daemon.SocketPath = socketPath
	cfg.Daemon.LogFile = ""

	d, err := daemon.New(cfg)
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	if err := d.Start(); err != nil {
		t.Fatalf("daemon.Start: %v", err)
	}
	t.Cleanup(func() {
		d.Stop()
		os.Remove(socketPath)
	})
}

// TestLoadAndResolve_TildeExpansion confirms ~ in data_repo_path is expanded.
func TestLoadAndResolve_TildeExpansion(t *testing.T) {
	t.Setenv("SUNDIAL_CONFIG", "")
	t.Setenv("SUNDIAL_DATA_REPO", "")

	cfgDir := t.TempDir()
	cfgPath := writeConfigFile(t, cfgDir, "data_repo_path: ~/sundial-tilde-test\n")

	cfg, _, err := config.LoadAndResolve(cfgPath, "")
	if err != nil {
		t.Fatalf("LoadAndResolve: %v", err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, "sundial-tilde-test")
	if cfg.DataRepo != want {
		t.Errorf("DataRepo = %q, want %q", cfg.DataRepo, want)
	}
}

// TestLoadAndResolve_UnknownFieldRejected confirms strict decoding surfaces a
// clear, field-naming error for a misplaced/unknown key.
func TestLoadAndResolve_UnknownFieldRejected(t *testing.T) {
	t.Setenv("SUNDIAL_CONFIG", "")
	t.Setenv("SUNDIAL_DATA_REPO", "")

	cfgDir := t.TempDir()
	cfgPath := writeConfigFile(t, cfgDir, "data_repo_path: /x\ndaemon:\n  wake:\n    enabledd: true\n")

	_, _, err := config.LoadAndResolve(cfgPath, "")
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "enabledd") {
		t.Errorf("error %q does not name the offending field 'enabledd'", err)
	}
}
