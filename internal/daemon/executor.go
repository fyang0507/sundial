package daemon

import (
	"bytes"
	"context"
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

// ExecuteOutcome reports what a single execute() call did so the fire cycle can
// branch. The three states are mutually exclusive in practice:
//
//   - Executed: the main command ran (FireCount bumped); advance normally.
//   - Deferred: a precondition held the fire back; arrange a backoff retry
//     instead of advancing the regular schedule.
//   - neither set: a gate (poll trigger check) skipped the command without it
//     being a precondition deferral; advance the schedule as usual.
type ExecuteOutcome struct {
	Executed bool
	Deferred bool
}

// ExecutionResult holds the outcome of running a schedule's command.
type ExecutionResult struct {
	ExitCode      int
	Duration      time.Duration
	StdoutPreview string
	StderrPreview string
	// TimedOut is true when an ExecTimeout fired and the command's process
	// group was killed before it exited on its own. ExitCode is -1 in that case.
	TimedOut bool
}

// execute runs the command for a schedule, spawning it via /bin/zsh, capturing
// output, and updating the runtime state and run log.
//
// Caller (fireDueSchedules) holds sched.mu across the whole fire cycle, so
// overlapping runs are prevented at that layer, not here.
//
// Two condition gates can short-circuit a fire:
//
//   - The precondition (any trigger type) is the OUTERMOST gate: a readiness
//     check run before everything else. If it exits non-zero the fire is
//     DEFERRED — the main command is skipped, FireCount is unchanged, and the
//     returned ExecuteOutcome has Deferred=true so the caller schedules a backoff
//     retry instead of advancing to the next regular fire.
//   - For poll triggers, the trigger command runs next as a condition gate. If it
//     exits non-zero, the main command is skipped (but this is NOT a deferral —
//     the poll simply advances to its next interval).
//
// Returns an ExecuteOutcome describing what happened so advanceSchedule can
// branch (a deferral must not advance the regular schedule).
func (d *Daemon) execute(sched *activeSchedule) ExecuteOutcome {
	// Precondition gate: the outermost readiness check, applied to every trigger
	// type before the poll check and before the main command. A non-zero exit
	// defers the fire (no execution, no FireCount bump) and signals the caller to
	// arrange a backoff retry.
	if sched.desired.Precondition != "" {
		if !d.checkPrecondition(sched) {
			return ExecuteOutcome{Deferred: true}
		}
		// Precondition passed: clear any pending retry state so the next fire
		// starts a fresh backoff sequence if it later defers.
		d.resetPreconditionState(sched)
	}

	// Poll trigger pre-check: run trigger command, skip main if exit != 0.
	// Timeout is handled by advanceSchedule — if the deadline has passed,
	// the schedule completes without firing.
	if sched.desired.Trigger.Type == model.TriggerTypePoll {
		if !d.runTriggerCheck(sched) {
			return ExecuteOutcome{Executed: false}
		}
	}

	if sched.desired.Detach {
		return ExecuteOutcome{Executed: d.executeDetached(sched)}
	}

	log.Printf("schedule %s (%s): executing command: %s",
		sched.desired.ID, sched.desired.Name, sched.desired.Command)

	// Resolve the per-schedule execution timeout. An empty string means no
	// timeout (today's unbounded behavior). A malformed value is logged and
	// treated as no timeout rather than aborting the fire — the schedule was
	// validated at add time, so this should be unreachable, but we degrade
	// gracefully instead of dropping the run.
	var timeout time.Duration
	if sched.desired.ExecTimeout != "" {
		parsed, err := time.ParseDuration(sched.desired.ExecTimeout)
		if err != nil {
			log.Printf("WARN: schedule %s: invalid exec_timeout %q, running without timeout: %v",
				sched.desired.ID, sched.desired.ExecTimeout, err)
		} else {
			timeout = parsed
		}
	}

	result := runCommandWithTimeout(sched.desired.Command, nil, timeout)

	if result.TimedOut {
		log.Printf("schedule %s (%s): command exceeded exec_timeout %s, killed (exit_code=%d, duration=%s)",
			sched.desired.ID, sched.desired.Name, sched.desired.ExecTimeout, result.ExitCode, result.Duration)
	} else {
		log.Printf("schedule %s (%s): completed, exit_code=%d, duration=%s",
			sched.desired.ID, sched.desired.Name, result.ExitCode, result.Duration)
	}

	// Update runtime state.
	now := time.Now()
	sched.runtime.LastFiredAt = &now
	sched.runtime.LastExitCode = &result.ExitCode
	sched.runtime.FireCount++

	if err := d.runtimeStore.Write(sched.runtime); err != nil {
		log.Printf("WARN: schedule %s: failed to persist runtime state after execution: %v",
			sched.desired.ID, err)
	}

	// Append fire entry to run log. A timed-out run is still a fire (the command
	// ran, it just didn't finish in time); the reason marks why it was cut short.
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
	if result.TimedOut {
		entry.Reason = "timeout"
		entry.StderrPreview = appendTimeoutNote(result.StderrPreview, sched.desired.ExecTimeout)
	}
	if err := d.runLogStore.Append(entry); err != nil {
		log.Printf("WARN: schedule %s: failed to append run log: %v",
			sched.desired.ID, err)
	}

	return ExecuteOutcome{Executed: true}
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

// checkPrecondition runs the schedule's precondition command as a readiness
// gate and returns true if it passed (exit 0). It mirrors runTriggerCheck's
// environment contract — SUNDIAL_SCHEDULE_ID and SUNDIAL_LAST_FIRED_AT are
// exported so the check can scope itself — but does NOT touch FireCount or
// CheckCount: a precondition is neither a fire nor a poll check. The backoff /
// give-up bookkeeping for a failed precondition lives in the scheduler's fire
// cycle (see advanceSchedule → deferPrecondition), not here.
func (d *Daemon) checkPrecondition(sched *activeSchedule) bool {
	cmd := sched.desired.Precondition

	log.Printf("schedule %s (%s): running precondition: %s",
		sched.desired.ID, sched.desired.Name, cmd)

	env := os.Environ()
	env = append(env, fmt.Sprintf("SUNDIAL_SCHEDULE_ID=%s", sched.desired.ID))
	if sched.runtime.LastFiredAt != nil {
		env = append(env, fmt.Sprintf("SUNDIAL_LAST_FIRED_AT=%s", sched.runtime.LastFiredAt.UTC().Format(time.RFC3339)))
	} else {
		env = append(env, "SUNDIAL_LAST_FIRED_AT=")
	}

	result := runCommandWithEnv(cmd, env)
	if result.ExitCode != 0 {
		log.Printf("schedule %s (%s): precondition returned exit %d, deferring fire",
			sched.desired.ID, sched.desired.Name, result.ExitCode)
		return false
	}

	log.Printf("schedule %s (%s): precondition passed, proceeding to fire",
		sched.desired.ID, sched.desired.Name)
	return true
}

// resetPreconditionState clears the per-fire precondition retry bookkeeping
// (attempt counter, first-deferred anchor, and pinned intended fire) and
// persists the runtime. Called after a precondition passes so the next pending
// fire starts a fresh backoff sequence. A no-op (no write) when nothing is set.
func (d *Daemon) resetPreconditionState(sched *activeSchedule) {
	if sched.runtime.PreconditionAttempts == 0 && sched.runtime.PreconditionFirstDeferredAt == nil {
		return
	}
	sched.runtime.PreconditionAttempts = 0
	sched.runtime.PreconditionFirstDeferredAt = nil
	sched.runtime.PreconditionIntendedFire = nil
	if err := d.runtimeStore.Write(sched.runtime); err != nil {
		log.Printf("WARN: schedule %s: failed to persist runtime after precondition reset: %v",
			sched.desired.ID, err)
	}
}

// runCommand executes a shell command via /bin/zsh and returns the result.
func runCommand(command string) ExecutionResult {
	return runCommandWithEnv(command, nil)
}

// runCommandWithEnv executes a shell command via /bin/zsh with optional extra
// environment variables. If env is nil, the current process environment is used.
func runCommandWithEnv(command string, env []string) ExecutionResult {
	return runCommandWithTimeout(command, env, 0)
}

// runCommandWithTimeout executes a shell command via /bin/zsh, optionally with
// extra environment variables and a wall-clock timeout. If env is nil, the
// current process environment is used. If timeout <= 0, the command runs to
// completion with no deadline (today's unbounded behavior).
//
// When a timeout is set, the command is launched in its own process group
// (Setpgid) so that on expiry we can SIGKILL the entire group (-pgid), not just
// the /bin/zsh shell — otherwise a hung child (e.g. a network call spawned by
// the script) would survive and the run would still be wedged. The returned
// ExecutionResult has TimedOut=true and ExitCode=-1 when the deadline fired.
func runCommandWithTimeout(command string, env []string, timeout time.Duration) ExecutionResult {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "/bin/zsh", "-l", "-c", command)
	if env != nil {
		cmd.Env = env
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &limitedWriter{buf: &stdoutBuf, limit: maxOutputCapture}
	cmd.Stderr = &limitedWriter{buf: &stderrBuf, limit: maxOutputCapture}

	if timeout > 0 {
		// Put the command in its own process group so the whole tree can be
		// killed on timeout. Cancel() below targets the negative pgid.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return nil
			}
			// Negative pid = "the whole process group". Falls back to killing
			// just the leader if the group lookup somehow fails.
			pgid, err := syscall.Getpgid(cmd.Process.Pid)
			if err != nil {
				return cmd.Process.Kill()
			}
			return syscall.Kill(-pgid, syscall.SIGKILL)
		}
	}

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	// ctx.Err() == DeadlineExceeded distinguishes a timeout kill from a command
	// that merely exited non-zero on its own.
	timedOut := timeout > 0 && ctx.Err() == context.DeadlineExceeded

	exitCode := 0
	switch {
	case timedOut:
		exitCode = -1
	case err != nil:
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
		TimedOut:      timedOut,
	}
}

// appendTimeoutNote annotates the captured stderr with a human-readable note
// that the run was killed by sundial's exec timeout, preserving any partial
// stderr the command produced before being killed.
func appendTimeoutNote(stderr, timeout string) string {
	note := fmt.Sprintf("[sundial: killed after exec_timeout %s]", timeout)
	if stderr == "" {
		return note
	}
	return stderr + "\n" + note
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
