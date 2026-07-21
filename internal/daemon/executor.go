package daemon

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/fyang0507/sundial/internal/model"
)

// maxOutputCapture is the maximum bytes captured from stdout/stderr.
const maxOutputCapture = 10 * 1024 // 10 KB

// shellInvocation describes a command to run through the user's LOGIN shell in
// one of two mutually-exclusive forms:
//
//   - Line (string / shell-line form): a shell command LINE, run verbatim as
//     `zsh -l -c <Line>`. Supports pipes, redirection, globs, and variable
//     expansion — but a bare path containing a space word-splits (issue #58).
//   - Args (argv-array form): an explicit argv, run as
//     `zsh -l -c 'exec "$@"' zsh <Args...>`. The positional params are passed as
//     distinct argv entries, so spaces never word-split and no shell quoting
//     needs to live in stored data. Prefer this for a script path with spaces.
//
// Args takes precedence over Line when non-empty. BOTH forms run under `zsh -l`
// so the login shell sources the user profile and rebuilds PATH — user tools
// (uv, codex, homebrew, ~/.local/bin) resolve at fire time either way. The
// array form does NOT drop the login shell; it only avoids re-parsing the
// command text.
type shellInvocation struct {
	Line string
	Args []string
}

// buildShellCommand is the single point where a command field is turned into an
// exec argv. Every execution path (runInvocation → runCommand/WithEnv/WithTimeout,
// the poll trigger check, the precondition, and executeDetached) funnels through
// here so the string-vs-array semantics stay in exactly one place.
//
// It returns the executable name and the argv to pass to exec.Command /
// exec.CommandContext.
func buildShellCommand(inv shellInvocation) (name string, args []string) {
	if len(inv.Args) > 0 {
		// Argv-array form. `exec "$@"` replaces the shell with the target program,
		// consuming the positional params ($1, $2, ...) that follow the literal
		// "zsh" (which becomes $0). Because each Args entry is a separate argv
		// word, a path like "/My Drive/script.zsh" is passed intact — no splitting.
		shellArgs := make([]string, 0, len(inv.Args)+4)
		shellArgs = append(shellArgs, "-l", "-c", `exec "$@"`, "zsh")
		shellArgs = append(shellArgs, inv.Args...)
		return "/bin/zsh", shellArgs
	}
	// String / shell-line form (backward-compatible default).
	return "/bin/zsh", []string{"-l", "-c", inv.Line}
}

// display renders an invocation for human-facing logs. The array form has no
// stored Line, so join the argv for readability (this string is NOT re-parsed —
// it is log output only).
func (inv shellInvocation) display() string {
	if len(inv.Args) > 0 {
		return strings.Join(inv.Args, " ")
	}
	return inv.Line
}

// ExecuteOutcome reports what a single execute() call did so the fire cycle can
// branch. The three states are mutually exclusive in practice:
//
//   - Executed: the main command ran (FireCount bumped); advance normally.
//   - Deferred: a precondition held the fire back; arrange a backoff retry
//     instead of advancing the regular schedule.
//   - Suppressed: the fire landed outside the active-hours window; skip the
//     command and defer NextFireAt to the next window opening (not a
//     precondition deferral — there is no backoff sequence).
//   - none set: a gate (poll trigger check) skipped the command without it
//     being a precondition deferral; advance the schedule as usual.
type ExecuteOutcome struct {
	Executed   bool
	Deferred   bool
	Suppressed bool
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
	// Active-hours gate: the OUTERMOST suppression, ahead of even the
	// precondition. NextFireAt is normally clamped into the window before we get
	// here, so this rarely triggers — it is the safety net for a within-grace
	// missed fire left in the past during closed hours, or a window that moved
	// (DST / refresh) between the advance and the fire. When it fires, the caller
	// defers NextFireAt to the next window opening; the command never runs.
	if sched.window != nil && !sched.window.Contains(time.Now()) {
		log.Printf("schedule %s (%s): due outside active hours %s, suppressing fire",
			sched.desired.ID, sched.desired.Name, sched.window.Describe())
		return ExecuteOutcome{Suppressed: true}
	}

	// Precondition gate: the outermost readiness check, applied to every trigger
	// type before the poll check and before the main command. A non-zero exit
	// defers the fire (no execution, no FireCount bump) and signals the caller to
	// arrange a backoff retry.
	if sched.desired.Precondition != "" || len(sched.desired.PreconditionArgs) > 0 {
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

	inv := shellInvocation{Line: sched.desired.Command, Args: sched.desired.CommandArgs}
	log.Printf("schedule %s (%s): executing command: %s",
		sched.desired.ID, sched.desired.Name, inv.display())

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

	result := runInvocation(inv, nil, timeout)

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
	inv := shellInvocation{Line: sched.desired.Command, Args: sched.desired.CommandArgs}
	log.Printf("schedule %s (%s): spawning detached command: %s",
		sched.desired.ID, sched.desired.Name, inv.display())

	name, cmdArgs := buildShellCommand(inv)
	cmd := exec.Command(name, cmdArgs...)
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
	inv := shellInvocation{
		Line: sched.desired.Trigger.TriggerCommand,
		Args: sched.desired.Trigger.TriggerCommandArgs,
	}

	log.Printf("schedule %s (%s): running trigger check: %s",
		sched.desired.ID, sched.desired.Name, inv.display())

	// Build environment variables for the trigger command.
	env := os.Environ()
	env = append(env, fmt.Sprintf("SUNDIAL_SCHEDULE_ID=%s", sched.desired.ID))
	if sched.runtime.LastFiredAt != nil {
		env = append(env, fmt.Sprintf("SUNDIAL_LAST_FIRED_AT=%s", sched.runtime.LastFiredAt.UTC().Format(time.RFC3339)))
	} else {
		env = append(env, "SUNDIAL_LAST_FIRED_AT=")
	}

	result := runInvocation(inv, env, 0)

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
	inv := shellInvocation{Line: sched.desired.Precondition, Args: sched.desired.PreconditionArgs}

	log.Printf("schedule %s (%s): running precondition: %s",
		sched.desired.ID, sched.desired.Name, inv.display())

	env := os.Environ()
	env = append(env, fmt.Sprintf("SUNDIAL_SCHEDULE_ID=%s", sched.desired.ID))
	if sched.runtime.LastFiredAt != nil {
		env = append(env, fmt.Sprintf("SUNDIAL_LAST_FIRED_AT=%s", sched.runtime.LastFiredAt.UTC().Format(time.RFC3339)))
	} else {
		env = append(env, "SUNDIAL_LAST_FIRED_AT=")
	}

	result := runInvocation(inv, env, 0)
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

// runCommand executes a shell-line command via /bin/zsh and returns the result.
func runCommand(command string) ExecutionResult {
	return runInvocation(shellInvocation{Line: command}, nil, 0)
}

// runCommandWithEnv executes a shell-line command via /bin/zsh with optional
// extra environment variables. If env is nil, the current process environment is
// used.
func runCommandWithEnv(command string, env []string) ExecutionResult {
	return runInvocation(shellInvocation{Line: command}, env, 0)
}

// runCommandWithTimeout executes a shell-line command via /bin/zsh, optionally
// with extra environment variables and a wall-clock timeout. Thin wrapper over
// runInvocation for the string form.
func runCommandWithTimeout(command string, env []string, timeout time.Duration) ExecutionResult {
	return runInvocation(shellInvocation{Line: command}, env, timeout)
}

// runInvocation executes a command (string OR argv-array form; see
// shellInvocation and buildShellCommand) via the login shell /bin/zsh -l,
// optionally with extra environment variables and a wall-clock timeout. If env
// is nil, the current process environment is used. If timeout <= 0, the command
// runs to completion with no deadline (today's unbounded behavior).
//
// When a timeout is set, the command is launched in its own process group
// (Setpgid) so that on expiry we can SIGKILL the entire group (-pgid), not just
// the /bin/zsh shell — otherwise a hung child (e.g. a network call spawned by
// the script) would survive and the run would still be wedged. The returned
// ExecutionResult has TimedOut=true and ExitCode=-1 when the deadline fired.
func runInvocation(inv shellInvocation, env []string, timeout time.Duration) ExecutionResult {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	name, cmdArgs := buildShellCommand(inv)
	cmd := exec.CommandContext(ctx, name, cmdArgs...)
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
