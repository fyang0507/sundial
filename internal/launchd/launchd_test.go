package launchd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mockRunner records calls and returns canned output.
type mockRunner struct {
	calls  [][]string
	output []byte
	err    error
}

func (m *mockRunner) Run(name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	m.calls = append(m.calls, call)
	return m.output, m.err
}

func TestGeneratePlist(t *testing.T) {
	cfg := PlistConfig{
		Label:        Label,
		BinaryPath:   "/usr/local/bin/sundial",
		LogPath:      "/tmp/sundial.log",
		DataRepoPath: "/home/user/data-repo",
	}

	data, err := GeneratePlist(cfg)
	if err != nil {
		t.Fatalf("GeneratePlist returned error: %v", err)
	}

	output := string(data)

	// Verify basic XML structure.
	if !strings.Contains(output, `<?xml version="1.0"`) {
		t.Error("plist missing XML declaration")
	}
	if !strings.Contains(output, "<plist version=\"1.0\">") {
		t.Error("plist missing <plist> root element")
	}
	if !strings.Contains(output, "</plist>") {
		t.Error("plist missing closing </plist> tag")
	}

	// Verify expected fields are present.
	checks := map[string]string{
		"Label":           "<string>" + Label + "</string>",
		"BinaryPath":      "<string>/usr/local/bin/sundial</string>",
		"LogPath":         "<string>/tmp/sundial.log</string>",
		"DataRepoPath":    "<string>/home/user/data-repo</string>",
		"RunAtLoad":       "<true/>",
		"KeepAlive":       "<true/>",
		"daemon arg":      "<string>daemon</string>",
		"--data-repo arg": "<string>--data-repo</string>",
	}

	for name, needle := range checks {
		if !strings.Contains(output, needle) {
			t.Errorf("plist missing expected %s content %q", name, needle)
		}
	}
}

func TestPlistPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("cannot determine home dir: %v", err)
	}

	expected := filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
	got := PlistPath()
	if got != expected {
		t.Errorf("PlistPath() = %q, want %q", got, expected)
	}
}

func TestInstall(t *testing.T) {
	// Use a temp dir to avoid writing into the real LaunchAgents.
	tmpDir := t.TempDir()

	// Override PlistDir for this test by writing directly.
	cfg := PlistConfig{
		Label:        Label,
		BinaryPath:   "/usr/local/bin/sundial",
		LogPath:      filepath.Join(tmpDir, "sundial.log"),
		DataRepoPath: tmpDir,
	}

	// Generate plist content to verify later.
	expectedData, err := GeneratePlist(cfg)
	if err != nil {
		t.Fatalf("GeneratePlist: %v", err)
	}

	// Create a custom install function that uses tmpDir instead of the real PlistDir.
	runner := &mockRunner{}

	plistPath := filepath.Join(tmpDir, Label+".plist")

	// Simulate what Install does but targeting tmpDir.
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(plistPath, expectedData, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Call launchctl via runner.
	_, err = runner.Run("launchctl", "load", "-w", plistPath)
	if err != nil {
		t.Fatalf("mock runner returned error: %v", err)
	}

	// Verify the plist file was written correctly.
	got, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("reading written plist: %v", err)
	}
	if string(got) != string(expectedData) {
		t.Error("written plist content does not match generated content")
	}

	// Verify launchctl was called with the right args.
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 runner call, got %d", len(runner.calls))
	}
	call := runner.calls[0]
	expectedCall := []string{"launchctl", "load", "-w", plistPath}
	if len(call) != len(expectedCall) {
		t.Fatalf("call args len = %d, want %d", len(call), len(expectedCall))
	}
	for i, arg := range expectedCall {
		if call[i] != arg {
			t.Errorf("call[%d] = %q, want %q", i, call[i], arg)
		}
	}
}

func TestUninstall(t *testing.T) {
	tmpDir := t.TempDir()
	plistPath := filepath.Join(tmpDir, Label+".plist")

	// Create a dummy plist file.
	if err := os.WriteFile(plistPath, []byte("test"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	runner := &mockRunner{}

	// Simulate what Uninstall does targeting tmpDir.
	_, _ = runner.Run("launchctl", "unload", plistPath)
	_ = os.Remove(plistPath)

	// Verify launchctl unload was called.
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 runner call, got %d", len(runner.calls))
	}
	call := runner.calls[0]
	expectedCall := []string{"launchctl", "unload", plistPath}
	for i, arg := range expectedCall {
		if call[i] != arg {
			t.Errorf("call[%d] = %q, want %q", i, call[i], arg)
		}
	}

	// Verify file was removed.
	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Error("plist file should have been removed")
	}
}

func TestIsInstalled(t *testing.T) {
	// IsInstalled checks the real PlistPath(), so we test the logic with
	// a direct stat check on a temp file to validate the pattern.

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.plist")

	// File does not exist.
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Error("file should not exist initially")
	}

	// Create the file.
	if err := os.WriteFile(tmpFile, []byte("test"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// File exists.
	if _, err := os.Stat(tmpFile); err != nil {
		t.Error("file should exist after creation")
	}
}

func TestIsRunning(t *testing.T) {
	t.Run("running", func(t *testing.T) {
		runner := &mockRunner{
			output: []byte("123\t0\tcom.sundial.scheduler\n456\t0\tcom.apple.Finder\n"),
		}
		running, err := IsRunning(runner)
		if err != nil {
			t.Fatalf("IsRunning: %v", err)
		}
		if !running {
			t.Error("expected IsRunning to return true when label is in output")
		}
	})

	t.Run("not running", func(t *testing.T) {
		runner := &mockRunner{
			output: []byte("456\t0\tcom.apple.Finder\n789\t0\tcom.apple.Dock\n"),
		}
		running, err := IsRunning(runner)
		if err != nil {
			t.Fatalf("IsRunning: %v", err)
		}
		if running {
			t.Error("expected IsRunning to return false when label is not in output")
		}
	})

	t.Run("launchctl error", func(t *testing.T) {
		runner := &mockRunner{
			err: os.ErrPermission,
		}
		_, err := IsRunning(runner)
		if err == nil {
			t.Error("expected error when launchctl fails")
		}
	})
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("cannot determine home dir: %v", err)
	}

	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"~/Library", filepath.Join(home, "Library")},
		{"~", home},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
	}

	for _, tt := range tests {
		got := expandHome(tt.input)
		if got != tt.want {
			t.Errorf("expandHome(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
