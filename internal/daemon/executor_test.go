package daemon

import (
	"strings"
	"testing"
)

func TestRunCommand_EchoHello(t *testing.T) {
	result := runCommand("echo hello")

	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}

	stdout := strings.TrimSpace(result.StdoutPreview)
	if stdout != "hello" {
		t.Errorf("expected stdout 'hello', got %q", stdout)
	}

	if result.Duration <= 0 {
		t.Error("expected positive duration")
	}
}

func TestRunCommand_ExitCode(t *testing.T) {
	result := runCommand("exit 42")

	if result.ExitCode != 42 {
		t.Errorf("expected exit code 42, got %d", result.ExitCode)
	}
}

func TestRunCommand_StderrCapture(t *testing.T) {
	result := runCommand("echo error_msg >&2")

	stderr := strings.TrimSpace(result.StderrPreview)
	if !strings.Contains(stderr, "error_msg") {
		t.Errorf("expected stderr to contain 'error_msg', got %q", stderr)
	}
}

func TestRunCommand_OutputTruncation(t *testing.T) {
	// Generate output larger than 10KB using printf which is a shell builtin
	// and avoids spawning external processes (more reliable under parallel test load).
	result := runCommand("printf '%0.sa]' {1..16000}")

	if len(result.StdoutPreview) > maxOutputCapture {
		t.Errorf("expected stdout to be capped at %d bytes, got %d",
			maxOutputCapture, len(result.StdoutPreview))
	}

	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0 even with truncation, got %d", result.ExitCode)
	}
}

func TestRunCommand_InvalidCommand(t *testing.T) {
	result := runCommand("command_that_does_not_exist_xyz_abc_123")

	if result.ExitCode == 0 {
		t.Error("expected non-zero exit code for invalid command")
	}
}
