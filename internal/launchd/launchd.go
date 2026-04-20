package launchd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

// Label is the launchd job label for the Sundial daemon.
const Label = "com.sundial.scheduler"

// PlistDir is the directory where the plist file is installed.
const PlistDir = "~/Library/LaunchAgents"

// PlistConfig holds all values needed to render a launchd plist.
type PlistConfig struct {
	Label        string
	BinaryPath   string
	LogPath      string
	DataRepoPath string
}

// CommandRunner abstracts command execution so tests can provide a mock.
type CommandRunner interface {
	Run(name string, args ...string) ([]byte, error)
}

type realRunner struct{}

func (r *realRunner) Run(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// DefaultRunner returns the real CommandRunner that executes via os/exec.
func DefaultRunner() CommandRunner {
	return &realRunner{}
}

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.BinaryPath}}</string>
        <string>daemon</string>
        <string>--data-repo</string>
        <string>{{.DataRepoPath}}</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>{{.LogPath}}</string>
    <key>StandardErrorPath</key>
    <string>{{.LogPath}}</string>
    <key>WorkingDirectory</key>
    <string>{{.DataRepoPath}}</string>
</dict>
</plist>
`

// GeneratePlist renders a launchd plist XML from the given config.
func GeneratePlist(cfg PlistConfig) ([]byte, error) {
	tmpl, err := template.New("plist").Parse(plistTemplate)
	if err != nil {
		return nil, fmt.Errorf("parsing plist template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return nil, fmt.Errorf("executing plist template: %w", err)
	}

	return buf.Bytes(), nil
}

// PlistPath returns the expanded absolute path to the Sundial plist file.
func PlistPath() string {
	return filepath.Join(expandHome(PlistDir), Label+".plist")
}

// Install generates the plist, writes it to disk, and loads it via launchctl.
func Install(cfg PlistConfig, runner CommandRunner) error {
	data, err := GeneratePlist(cfg)
	if err != nil {
		return fmt.Errorf("generating plist: %w", err)
	}

	dir := expandHome(PlistDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating LaunchAgents directory %s: %w", dir, err)
	}

	ppath := PlistPath()
	if err := os.WriteFile(ppath, data, 0644); err != nil {
		return fmt.Errorf("writing plist to %s: %w", ppath, err)
	}

	out, err := runner.Run("launchctl", "load", "-w", ppath)
	if err != nil {
		return fmt.Errorf("launchctl load failed: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return nil
}

// Uninstall unloads the daemon from launchd and removes the plist file.
// Errors from unloading (e.g. not loaded) and removing (e.g. file missing)
// are silently ignored.
func Uninstall(runner CommandRunner) error {
	ppath := PlistPath()

	// Best-effort unload; ignore errors if the job is not loaded.
	_, _ = runner.Run("launchctl", "unload", ppath)

	// Best-effort remove; ignore errors if the file doesn't exist.
	_ = os.Remove(ppath)

	return nil
}

// IsInstalled reports whether the plist file exists on disk.
func IsInstalled() bool {
	_, err := os.Stat(PlistPath())
	return err == nil
}

// IsRunning checks launchctl list output for the Sundial label.
func IsRunning(runner CommandRunner) (bool, error) {
	out, err := runner.Run("launchctl", "list")
	if err != nil {
		return false, fmt.Errorf("launchctl list failed: %w", err)
	}
	return strings.Contains(string(out), Label), nil
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(p string) string {
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
