package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/fyang0507/sundial/internal/model"
	"github.com/fyang0507/sundial/internal/trigger"
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

func TestRunCommandWithEnv(t *testing.T) {
	env := []string{
		"PATH=/usr/bin:/bin",
		"SUNDIAL_TEST_VAR=hello_world",
	}
	result := runCommandWithEnv("echo $SUNDIAL_TEST_VAR", env)

	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}

	stdout := strings.TrimSpace(result.StdoutPreview)
	if stdout != "hello_world" {
		t.Errorf("expected stdout 'hello_world', got %q", stdout)
	}
}

func TestRunCommandWithEnv_NilEnv(t *testing.T) {
	// nil env should use the current process environment (same as runCommand).
	result := runCommandWithEnv("echo hello", nil)

	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}

	stdout := strings.TrimSpace(result.StdoutPreview)
	if stdout != "hello" {
		t.Errorf("expected stdout 'hello', got %q", stdout)
	}
}

func TestRunCommandWithTimeout_FastCommandCompletes(t *testing.T) {
	// A command that finishes well within the timeout returns its real exit
	// code and is not flagged as timed out.
	result := runCommandWithTimeout("echo done; exit 7", nil, 5*time.Second)

	if result.TimedOut {
		t.Error("expected TimedOut=false for a command that finished in time")
	}
	if result.ExitCode != 7 {
		t.Errorf("expected exit code 7, got %d", result.ExitCode)
	}
	if stdout := strings.TrimSpace(result.StdoutPreview); stdout != "done" {
		t.Errorf("expected stdout 'done', got %q", stdout)
	}
}

func TestRunCommandWithTimeout_HungCommandKilled(t *testing.T) {
	// A long sleep with a short timeout must be killed promptly, report a
	// nonzero (-1) exit code, and be flagged as timed out.
	timeout := 200 * time.Millisecond
	start := time.Now()
	result := runCommandWithTimeout("sleep 10", nil, timeout)
	elapsed := time.Since(start)

	if !result.TimedOut {
		t.Error("expected TimedOut=true for a command that exceeded the timeout")
	}
	if result.ExitCode != -1 {
		t.Errorf("expected exit code -1 on timeout, got %d", result.ExitCode)
	}
	// Should be killed roughly at the deadline, not run the full 10s sleep.
	if elapsed > 3*time.Second {
		t.Errorf("expected command to be killed near the timeout, took %s", elapsed)
	}
}

func TestExecute_ExecTimeoutKillsAndLogsTimeout(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_exec_timeout01", "timeout-exec", "0 9 * * *")
	desired.Command = "sleep 10"
	desired.ExecTimeout = "200ms"
	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	runtime := &model.RuntimeState{
		ID:         "sch_exec_timeout01",
		NextFireAt: time.Now(),
	}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}

	sched := &activeSchedule{
		desired: desired,
		runtime: runtime,
		trigger: trig,
	}

	d.mu.Lock()
	d.schedules["sch_exec_timeout01"] = sched
	d.mu.Unlock()

	start := time.Now()
	fired := d.execute(sched).Executed
	elapsed := time.Since(start)

	if !fired {
		t.Error("expected execute to return true (the command ran, then timed out)")
	}
	if elapsed > 3*time.Second {
		t.Errorf("expected execute to return near the timeout, took %s", elapsed)
	}
	if sched.runtime.LastExitCode == nil {
		t.Fatal("expected LastExitCode to be set")
	}
	if *sched.runtime.LastExitCode != -1 {
		t.Errorf("expected LastExitCode=-1 on timeout, got %d", *sched.runtime.LastExitCode)
	}

	// The fire entry must record the timeout reason.
	entries, err := d.runLogStore.Read("sch_exec_timeout01")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 run log entry, got %d", len(entries))
	}
	if entries[0].Reason != "timeout" {
		t.Errorf("expected run log reason 'timeout', got %q", entries[0].Reason)
	}
	if !strings.Contains(entries[0].StderrPreview, "exec_timeout") {
		t.Errorf("expected stderr preview to note the exec_timeout, got %q", entries[0].StderrPreview)
	}
}

func TestExecute_NoExecTimeoutRunsToCompletion(t *testing.T) {
	// With no ExecTimeout set, a quick command runs to completion and is never
	// flagged as a timeout — the default unbounded path is preserved.
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_exec_notimeout01", "no-timeout-exec", "0 9 * * *")
	desired.Command = "exit 3"
	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	runtime := &model.RuntimeState{
		ID:         "sch_exec_notimeout01",
		NextFireAt: time.Now(),
	}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}

	sched := &activeSchedule{
		desired: desired,
		runtime: runtime,
		trigger: trig,
	}

	d.mu.Lock()
	d.schedules["sch_exec_notimeout01"] = sched
	d.mu.Unlock()

	if !d.execute(sched).Executed {
		t.Error("expected execute to return true")
	}
	if sched.runtime.LastExitCode == nil || *sched.runtime.LastExitCode != 3 {
		t.Errorf("expected LastExitCode=3, got %v", sched.runtime.LastExitCode)
	}
	entries, err := d.runLogStore.Read("sch_exec_notimeout01")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 run log entry, got %d", len(entries))
	}
	if entries[0].Reason != "" {
		t.Errorf("expected empty run log reason, got %q", entries[0].Reason)
	}
}

func TestExecute_PollTriggerCheckPasses(t *testing.T) {
	d := newTestDaemon(t)

	desired := makePollDesired("sch_exec_poll01", "poll-exec-pass", "exit 0", "2m")
	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	runtime := &model.RuntimeState{
		ID:         "sch_exec_poll01",
		NextFireAt: time.Now(),
	}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}

	sched := &activeSchedule{
		desired: desired,
		runtime: runtime,
		trigger: trig,
	}

	d.mu.Lock()
	d.schedules["sch_exec_poll01"] = sched
	d.mu.Unlock()

	fired := d.execute(sched).Executed

	if !fired {
		t.Error("expected execute to return true when trigger check passes")
	}
	if sched.runtime.FireCount != 1 {
		t.Errorf("expected FireCount=1, got %d", sched.runtime.FireCount)
	}
	if sched.runtime.CheckCount != 1 {
		t.Errorf("expected CheckCount=1, got %d", sched.runtime.CheckCount)
	}
}

func TestExecute_PollTriggerCheckFails(t *testing.T) {
	d := newTestDaemon(t)

	desired := makePollDesired("sch_exec_poll02", "poll-exec-fail", "exit 1", "2m")
	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	runtime := &model.RuntimeState{
		ID:         "sch_exec_poll02",
		NextFireAt: time.Now(),
	}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}

	sched := &activeSchedule{
		desired: desired,
		runtime: runtime,
		trigger: trig,
	}

	d.mu.Lock()
	d.schedules["sch_exec_poll02"] = sched
	d.mu.Unlock()

	fired := d.execute(sched).Executed

	if fired {
		t.Error("expected execute to return false when trigger check fails")
	}
	if sched.runtime.FireCount != 0 {
		t.Errorf("expected FireCount=0, got %d", sched.runtime.FireCount)
	}
	if sched.runtime.CheckCount != 1 {
		t.Errorf("expected CheckCount=1, got %d", sched.runtime.CheckCount)
	}
	if sched.runtime.LastFiredAt != nil {
		t.Error("expected LastFiredAt to be nil when trigger check fails")
	}
}

func TestExecute_DetachedReturnsImmediately(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_exec_detach01", "detach-exec", "0 9 * * *")
	// Long-running command: if execute waited, this test would block for ~10s.
	desired.Command = "sleep 10"
	desired.Detach = true
	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	runtime := &model.RuntimeState{
		ID:         "sch_exec_detach01",
		NextFireAt: time.Now(),
	}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}

	sched := &activeSchedule{
		desired: desired,
		runtime: runtime,
		trigger: trig,
	}

	d.mu.Lock()
	d.schedules["sch_exec_detach01"] = sched
	d.mu.Unlock()

	start := time.Now()
	fired := d.execute(sched).Executed
	elapsed := time.Since(start)

	if !fired {
		t.Error("expected execute to return true for detached schedule")
	}
	if elapsed > 2*time.Second {
		t.Errorf("expected detached execute to return quickly, took %s", elapsed)
	}
	if sched.runtime.FireCount != 1 {
		t.Errorf("expected FireCount=1, got %d", sched.runtime.FireCount)
	}
	if sched.runtime.LastFiredAt == nil {
		t.Error("expected LastFiredAt to be set")
	}
	if sched.runtime.LastExitCode != nil {
		t.Errorf("expected LastExitCode to remain nil for detached, got %d", *sched.runtime.LastExitCode)
	}
}

func TestExecute_DetachedMutexReleasedImmediately(t *testing.T) {
	// The whole point of --detach is that the per-schedule mutex is released
	// as soon as the spawn returns, so a nested `add --refresh` from within
	// the callback doesn't see a "schedule currently firing" window.
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_exec_detach02", "detach-mutex", "0 9 * * *")
	desired.Command = "sleep 10"
	desired.Detach = true
	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	runtime := &model.RuntimeState{
		ID:         "sch_exec_detach02",
		NextFireAt: time.Now(),
	}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}

	sched := &activeSchedule{
		desired: desired,
		runtime: runtime,
		trigger: trig,
	}

	d.mu.Lock()
	d.schedules["sch_exec_detach02"] = sched
	d.mu.Unlock()

	d.execute(sched)

	// After execute returns, the per-schedule mutex should be free.
	if !sched.mu.TryLock() {
		t.Error("expected per-schedule mutex to be released after detached execute")
	} else {
		sched.mu.Unlock()
	}
}

func TestExecute_NonPollReturnsTrue(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_exec_cron01", "cron-exec", "0 9 * * *")
	desired.Command = "echo test"
	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	runtime := &model.RuntimeState{
		ID:         "sch_exec_cron01",
		NextFireAt: time.Now(),
	}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}

	sched := &activeSchedule{
		desired: desired,
		runtime: runtime,
		trigger: trig,
	}

	d.mu.Lock()
	d.schedules["sch_exec_cron01"] = sched
	d.mu.Unlock()

	fired := d.execute(sched).Executed

	if !fired {
		t.Error("expected execute to return true for cron trigger")
	}
	if sched.runtime.FireCount != 1 {
		t.Errorf("expected FireCount=1, got %d", sched.runtime.FireCount)
	}
}
