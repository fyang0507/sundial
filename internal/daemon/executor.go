package daemon

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"syscall"
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

// execute runs the command for a schedule, spawning it via /bin/zsh, capturing
// output, and updating the runtime state and run log.
//
// Caller (fireDueSchedules) holds sched.mu across the whole fire cycle, so
// overlapping runs are prevented at that layer, not here.
//
// For poll triggers, a trigger command is run first as a condition gate. If it
// exits non-zero, the main command is skipped and false is returned.
// Returns true if the main command was executed.
func (d *Daemon) execute(sched *activeSchedule) bool {
	// Poll trigger pre-check: run trigger command, skip main if exit != 0.
	// Timeout is handled by advanceSchedule — if the deadline has passed,
	// the schedule completes without firing.
	if sched.desired.Trigger.Type == model.TriggerTypePoll {
		if !d.runTriggerCheck(sched) {
			return false
		}
	}

	if sched.desired.Detach {
		return d.executeDetached(sched)
	}

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

	return true
}

// executeDetached spawns the command without waiting for it to exit. This
// collapses the firing window to the time it takes to Start() the process,
// so callbacks can safely re-enter the daemon (e.g. `sundial add --refresh`)
// without tripping the "schedule currently firing" serialization.
//
// The child is placed in its own session (Setsid) so it survives daemon
// restarts and is not killed if launchd signals the daemon's process group.
// LastExitCode stays nil — no sundial-side visibility into outcome.
func (d *Daemon) executeDetached(sched *activeSchedule) bool {
	log.Printf("schedule %s (%s): spawning detached command: %s",
		sched.desired.ID, sched.desired.Name, sched.desired.Command)

	cmd := exec.Command("/bin/zsh", "-l", "-c", sched.desired.Command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	now := time.Now()
	if err := cmd.Start(); err != nil {
		log.Printf("schedule %s (%s): detached spawn failed: %v",
			sched.desired.ID, sched.desired.Name, err)
		return false
	}

	// Reap the child asynchronously so it doesn't become a zombie. We discard
	// the exit code — that's the whole point of --detach.
	go func() {
		_ = cmd.Wait()
	}()

	log.Printf("schedule %s (%s): detached child pid=%d",
		sched.desired.ID, sched.desired.Name, cmd.Process.Pid)

	// Update runtime state — LastExitCode stays nil.
	sched.runtime.LastFiredAt = &now
	sched.runtime.LastExitCode = nil
	sched.runtime.FireCount++

	if err := d.runtimeStore.Write(sched.runtime); err != nil {
		log.Printf("WARN: schedule %s: failed to persist runtime state after detached spawn: %v",
			sched.desired.ID, err)
	}

	// Append a fire entry without exit code or duration.
	entry := &model.RunLogEntry{
		Timestamp:  now,
		Type:       model.LogTypeFire,
		ScheduleID: sched.desired.ID,
		Reason:     "detached",
	}
	if err := d.runLogStore.Append(entry); err != nil {
		log.Printf("WARN: schedule %s: failed to append run log: %v",
			sched.desired.ID, err)
	}

	return true
}

// isPollTimedOut returns true if the poll schedule's timeout has expired.
// The deadline is computed as created_at + timeout from the trigger config.
func (d *Daemon) isPollTimedOut(sched *activeSchedule) bool {
	timeoutStr := sched.desired.Trigger.Timeout
	if timeoutStr == "" {
		return false
	}
	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		log.Printf("WARN: schedule %s: invalid timeout %q: %v", sched.desired.ID, timeoutStr, err)
		return false
	}
	deadline := sched.desired.CreatedAt.Add(timeout)
	return time.Now().After(deadline)
}

// runTriggerCheck executes the poll trigger's condition command and returns
// true if the condition passed (exit code 0). It increments CheckCount and
// passes SUNDIAL_SCHEDULE_ID and SUNDIAL_LAST_FIRED_AT as environment variables.
func (d *Daemon) runTriggerCheck(sched *activeSchedule) bool {
	trigCmd := sched.desired.Trigger.TriggerCommand

	log.Printf("schedule %s (%s): running trigger check: %s",
		sched.desired.ID, sched.desired.Name, trigCmd)

	// Build environment variables for the trigger command.
	env := os.Environ()
	env = append(env, fmt.Sprintf("SUNDIAL_SCHEDULE_ID=%s", sched.desired.ID))
	if sched.runtime.LastFiredAt != nil {
		env = append(env, fmt.Sprintf("SUNDIAL_LAST_FIRED_AT=%s", sched.runtime.LastFiredAt.UTC().Format(time.RFC3339)))
	} else {
		env = append(env, "SUNDIAL_LAST_FIRED_AT=")
	}

	result := runCommandWithEnv(trigCmd, env)

	sched.runtime.CheckCount++
	if err := d.runtimeStore.Write(sched.runtime); err != nil {
		log.Printf("WARN: schedule %s: failed to persist runtime state after check: %v",
			sched.desired.ID, err)
	}

	if result.ExitCode != 0 {
		log.Printf("schedule %s (%s): trigger check returned exit %d, skipping command (check #%d)",
			sched.desired.ID, sched.desired.Name, result.ExitCode, sched.runtime.CheckCount)
		return false
	}

	log.Printf("schedule %s (%s): trigger check passed (check #%d), proceeding to command",
		sched.desired.ID, sched.desired.Name, sched.runtime.CheckCount)
	return true
}

// runCommand executes a shell command via /bin/zsh and returns the result.
func runCommand(command string) ExecutionResult {
	return runCommandWithEnv(command, nil)
}

// runCommandWithEnv executes a shell command via /bin/zsh with optional extra
// environment variables. If env is nil, the current process environment is used.
func runCommandWithEnv(command string, env []string) ExecutionResult {
	cmd := exec.Command("/bin/zsh", "-l", "-c", command)
	if env != nil {
		cmd.Env = env
	}

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
