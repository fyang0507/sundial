package daemon

import (
	"bytes"
	"io"
	"log"
	"os/exec"
	"time"

	"github.com/fyang0507/sundial/internal/model"
)

// maxOutputCapture is the maximum bytes captured from stdout/stderr.
const maxOutputCapture = 10 * 1024 // 10 KB

// ExecutionResult holds the outcome of running a schedule's command.
type ExecutionResult struct {
	ExitCode      int
	Duration      time.Duration
	StdoutPreview string
	StderrPreview string
}

// execute runs the command for a schedule. It acquires the per-schedule mutex
// to prevent overlapping runs, spawns the command via /bin/zsh, captures output,
// and updates the runtime state and run log.
func (d *Daemon) execute(sched *activeSchedule) {
	// TryLock: if already running, skip.
	if !sched.mu.TryLock() {
		log.Printf("schedule %s (%s): skipping, previous execution still running",
			sched.desired.ID, sched.desired.Name)
		return
	}
	defer sched.mu.Unlock()

	log.Printf("schedule %s (%s): executing command: %s",
		sched.desired.ID, sched.desired.Name, sched.desired.Command)

	result := runCommand(sched.desired.Command)

	log.Printf("schedule %s (%s): completed, exit_code=%d, duration=%s",
		sched.desired.ID, sched.desired.Name, result.ExitCode, result.Duration)

	// Update runtime state.
	now := time.Now()
	sched.runtime.LastFiredAt = &now
	sched.runtime.LastExitCode = &result.ExitCode
	sched.runtime.FireCount++

	if err := d.runtimeStore.Write(sched.runtime); err != nil {
		log.Printf("WARN: schedule %s: failed to persist runtime state after execution: %v",
			sched.desired.ID, err)
	}

	// Append fire entry to run log.
	durationSec := result.Duration.Seconds()
	entry := &model.RunLogEntry{
		Timestamp:     now,
		Type:          model.LogTypeFire,
		ScheduleID:    sched.desired.ID,
		ExitCode:      &result.ExitCode,
		DurationSec:   &durationSec,
		StdoutPreview: result.StdoutPreview,
		StderrPreview: result.StderrPreview,
	}
	if err := d.runLogStore.Append(entry); err != nil {
		log.Printf("WARN: schedule %s: failed to append run log: %v",
			sched.desired.ID, err)
	}
}

// runCommand executes a shell command via /bin/zsh and returns the result.
func runCommand(command string) ExecutionResult {
	cmd := exec.Command("/bin/zsh", "-l", "-c", command)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &limitedWriter{buf: &stdoutBuf, limit: maxOutputCapture}
	cmd.Stderr = &limitedWriter{buf: &stderrBuf, limit: maxOutputCapture}

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// Command failed to start or other error.
			exitCode = -1
		}
	}

	return ExecutionResult{
		ExitCode:      exitCode,
		Duration:      duration,
		StdoutPreview: stdoutBuf.String(),
		StderrPreview: stderrBuf.String(),
	}
}

// limitedWriter wraps a bytes.Buffer and stops writing after limit bytes.
type limitedWriter struct {
	buf   *bytes.Buffer
	limit int
}

func (w *limitedWriter) Write(p []byte) (n int, err error) {
	remaining := w.limit - w.buf.Len()
	if remaining <= 0 {
		// Discard further writes but report success to avoid breaking the command.
		return len(p), nil
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	return w.buf.Write(p)
}

// Ensure limitedWriter implements io.Writer.
var _ io.Writer = (*limitedWriter)(nil)
