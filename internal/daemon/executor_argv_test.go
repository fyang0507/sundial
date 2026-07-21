package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fyang0507/sundial/internal/model"
	"github.com/fyang0507/sundial/internal/trigger"
)

// writeSpacePathScript creates an executable script under a directory whose name
// contains a space and returns its absolute path. This is the exact shape that
// broke in issue #58 (e.g. ".../My Drive/.../script.sh").
func writeSpacePathScript(t *testing.T, exitCode int) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dir with space")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "script.sh")
	body := fmt.Sprintf("#!/bin/sh\necho ran\nexit %d\n", exitCode)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

// --- buildShellCommand: the single point of command construction --------------

func TestBuildShellCommand_StringForm(t *testing.T) {
	name, args := buildShellCommand(shellInvocation{Line: "echo hi | cat"})
	if name != "/bin/zsh" {
		t.Errorf("expected /bin/zsh, got %q", name)
	}
	want := []string{"-l", "-c", "echo hi | cat"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("string form argv mismatch:\n got %#v\nwant %#v", args, want)
	}
}

func TestBuildShellCommand_ArrayForm(t *testing.T) {
	name, args := buildShellCommand(shellInvocation{
		Args: []string{"/My Drive/run.zsh", "--flag", "value with space"},
	})
	if name != "/bin/zsh" {
		t.Errorf("expected /bin/zsh, got %q", name)
	}
	want := []string{"-l", "-c", `exec "$@"`, "zsh", "/My Drive/run.zsh", "--flag", "value with space"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("array form argv mismatch:\n got %#v\nwant %#v", args, want)
	}
}

// TestBuildShellCommand_BothFormsUseLoginShell is the guardrail for the issue #58
// HARD CONSTRAINT: BOTH forms must go through `zsh -l` so the login shell rebuilds
// PATH. If a future change drops "-l" (or execs argv directly), this fails.
func TestBuildShellCommand_BothFormsUseLoginShell(t *testing.T) {
	for _, inv := range []shellInvocation{
		{Line: "echo hi"},
		{Args: []string{"/bin/echo", "hi"}},
	} {
		name, args := buildShellCommand(inv)
		if name != "/bin/zsh" {
			t.Fatalf("expected /bin/zsh, got %q", name)
		}
		if len(args) == 0 || args[0] != "-l" {
			t.Errorf("expected login shell (-l as first arg) for %#v, got %#v", inv, args)
		}
	}
}

// --- argv-array form fixes the space-path bug --------------------------------

func TestRunInvocation_ArrayFormSpacePath(t *testing.T) {
	script := writeSpacePathScript(t, 17)

	result := runInvocation(shellInvocation{Args: []string{script}}, nil, 0)

	if result.TimedOut {
		t.Error("did not expect a timeout")
	}
	if result.ExitCode != 17 {
		t.Errorf("expected exit code 17 from the space-path script, got %d", result.ExitCode)
	}
	if got := strings.TrimSpace(result.StdoutPreview); got != "ran" {
		t.Errorf("expected stdout %q, got %q", "ran", got)
	}
}

func TestRunInvocation_ArrayFormSpacePathWithArgs(t *testing.T) {
	// A space-containing argument (not just the path) must also survive intact:
	// the script exits with the length of $1, proving it received the whole
	// "a b c" as a single argv word rather than three.
	dir := filepath.Join(t.TempDir(), "dir with space")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "argcount.sh")
	// exit with the number of positional args; if "a b c" word-split it'd be 3.
	body := "#!/bin/sh\nexit $#\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	result := runInvocation(shellInvocation{Args: []string{script, "a b c"}}, nil, 0)
	if result.ExitCode != 1 {
		t.Errorf("expected exactly 1 argument to reach the script (exit 1), got exit %d", result.ExitCode)
	}
}

// TestRunInvocation_StringFormSpacePathFails documents the pre-existing (and
// preserved) string-form semantics that motivated the fix: a bare space-path run
// as a shell LINE word-splits and dies with exit 127. The array form is the fix.
func TestRunInvocation_StringFormSpacePathFails(t *testing.T) {
	script := writeSpacePathScript(t, 0)

	result := runInvocation(shellInvocation{Line: script}, nil, 0)
	if result.ExitCode != 127 {
		t.Errorf("expected the string form of a bare space-path to fail with exit 127, got %d", result.ExitCode)
	}
}

// TestRunInvocation_LoginShellPathRetained_ArrayForm proves the HARD CONSTRAINT:
// even with PATH emptied in the passed environment, a bare tool name resolves
// through the array form because `zsh -l` rebuilds PATH from the user profile
// (macOS path_helper). A fix that dropped the login shell would fail this.
func TestRunInvocation_LoginShellPathRetained_ArrayForm(t *testing.T) {
	env := []string{"PATH=", "HOME=" + os.Getenv("HOME")}

	result := runInvocation(shellInvocation{Args: []string{"uname"}}, env, 0)
	if result.ExitCode != 0 {
		t.Fatalf("expected bare 'uname' to resolve via the rebuilt login PATH (exit 0), got exit %d (stderr: %q)",
			result.ExitCode, strings.TrimSpace(result.StderrPreview))
	}
	if got := strings.TrimSpace(result.StdoutPreview); got == "" {
		t.Error("expected uname to produce output")
	}
}

func TestRunInvocation_LoginShellPathRetained_StringForm(t *testing.T) {
	env := []string{"PATH=", "HOME=" + os.Getenv("HOME")}

	result := runInvocation(shellInvocation{Line: "uname"}, env, 0)
	if result.ExitCode != 0 {
		t.Fatalf("expected string-form 'uname' to resolve via login PATH (exit 0), got exit %d", result.ExitCode)
	}
}

// --- execute() end-to-end with the array form --------------------------------

func TestExecute_ArrayFormCommandRuns(t *testing.T) {
	d := newTestDaemon(t)
	script := writeSpacePathScript(t, 0)

	desired := makeCronDesired("sch_exec_argv01", "argv-exec", "0 9 * * *")
	desired.Command = "" // array form: no shell-line
	desired.CommandArgs = []string{script}
	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	runtime := &model.RuntimeState{ID: desired.ID, NextFireAt: time.Now()}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}
	sched := &activeSchedule{desired: desired, runtime: runtime, trigger: trig}

	d.mu.Lock()
	d.schedules[desired.ID] = sched
	d.mu.Unlock()

	if !d.execute(sched).Executed {
		t.Fatal("expected array-form command to execute")
	}
	if sched.runtime.FireCount != 1 {
		t.Errorf("expected FireCount=1, got %d", sched.runtime.FireCount)
	}
	if sched.runtime.LastExitCode == nil || *sched.runtime.LastExitCode != 0 {
		t.Errorf("expected LastExitCode=0, got %v", sched.runtime.LastExitCode)
	}
}

func TestExecute_ArrayFormPreconditionGate(t *testing.T) {
	d := newTestDaemon(t)
	// Precondition script (space path) that exits non-zero => fire is deferred.
	precond := writeSpacePathScript(t, 1)

	desired := makeCronDesired("sch_exec_argv_pre01", "argv-precond", "0 9 * * *")
	desired.Command = "echo should-not-run"
	desired.PreconditionArgs = []string{precond}
	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	runtime := &model.RuntimeState{ID: desired.ID, NextFireAt: time.Now()}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}
	sched := &activeSchedule{desired: desired, runtime: runtime, trigger: trig}

	d.mu.Lock()
	d.schedules[desired.ID] = sched
	d.mu.Unlock()

	outcome := d.execute(sched)
	if !outcome.Deferred {
		t.Error("expected the fire to be deferred by the failing array-form precondition")
	}
	if sched.runtime.FireCount != 0 {
		t.Errorf("expected FireCount=0 (precondition gated), got %d", sched.runtime.FireCount)
	}
}

// --- JSON backward compatibility ---------------------------------------------

func TestDesiredState_LegacyStringFormDeserializesAndRuns(t *testing.T) {
	// A schedule file written before this change has no *_args keys.
	legacy := `{
		"id": "sch_legacy01",
		"name": "legacy",
		"trigger": {"type": "cron", "cron": "0 9 * * *"},
		"command": "echo legacy-ok",
		"status": "active"
	}`

	var ds model.DesiredState
	if err := json.Unmarshal([]byte(legacy), &ds); err != nil {
		t.Fatalf("legacy JSON failed to deserialize: %v", err)
	}
	if ds.Command != "echo legacy-ok" {
		t.Errorf("expected Command preserved, got %q", ds.Command)
	}
	if ds.CommandArgs != nil {
		t.Errorf("expected CommandArgs nil for legacy JSON, got %#v", ds.CommandArgs)
	}

	// ...and it still runs through the executor unchanged.
	result := runInvocation(shellInvocation{Line: ds.Command, Args: ds.CommandArgs}, nil, 0)
	if result.ExitCode != 0 {
		t.Errorf("expected legacy command to run (exit 0), got %d", result.ExitCode)
	}
	if got := strings.TrimSpace(result.StdoutPreview); got != "legacy-ok" {
		t.Errorf("expected stdout %q, got %q", "legacy-ok", got)
	}
}

func TestDesiredState_ArrayFormRoundTrips(t *testing.T) {
	ds := model.DesiredState{
		ID:          "sch_argv01",
		Name:        "argv",
		Trigger:     model.TriggerConfig{Type: model.TriggerTypeCron, Cron: "0 9 * * *"},
		CommandArgs: []string{"/Users/x/My Drive/scripts/backup.zsh", "--flag", "value with space"},
		Status:      model.StatusActive,
	}

	data, err := json.Marshal(ds)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"command_args"`) {
		t.Errorf("expected command_args key in JSON, got %s", data)
	}

	var back model.DesiredState
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.CommandArgs, ds.CommandArgs) {
		t.Errorf("CommandArgs did not round-trip:\n got %#v\nwant %#v", back.CommandArgs, ds.CommandArgs)
	}
	if back.Command != "" {
		t.Errorf("expected empty Command for array form, got %q", back.Command)
	}
}
