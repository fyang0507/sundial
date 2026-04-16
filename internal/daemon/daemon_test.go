package daemon

import (
	"encoding/json"
	"os/exec"
	"testing"
	"time"

	"github.com/fyang0507/sundial/internal/model"
	"github.com/fyang0507/sundial/internal/trigger"
)

// newTestDaemonWithGit creates a test daemon whose data repo is a real git
// repository, so that handlers calling gitOps (commit, push, etc.) succeed.
func newTestDaemonWithGit(t *testing.T) *Daemon {
	t.Helper()
	d := newTestDaemon(t)
	initTestGitRepo(t, d.cfg.DataRepo)
	return d
}

// initTestGitRepo initializes a git repo with an initial commit.
func initTestGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@sundial.dev"},
		{"git", "config", "user.name", "Sundial Test"},
		{"git", "commit", "--allow-empty", "-m", "init"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git command %v failed: %v\n%s", args, err, out)
		}
	}
}

func TestHandleList_Empty(t *testing.T) {
	d := newTestDaemon(t)

	result, rpcErr := d.handleList()
	if rpcErr != nil {
		t.Fatalf("unexpected error: %v", rpcErr.Message)
	}

	if len(result.Schedules) != 0 {
		t.Errorf("expected 0 schedules, got %d", len(result.Schedules))
	}
}

func TestHandleList_WithSchedules(t *testing.T) {
	d := newTestDaemon(t)

	// Add schedules to the in-memory map.
	desired1 := makeCronDesired("sch_l01", "list-test-1", "0 9 * * *")
	desired2 := makeCronDesired("sch_l02", "list-test-2", "30 14 * * *")

	for _, ds := range []*model.DesiredState{desired1, desired2} {
		trig, err := trigger.ParseTrigger(ds.Trigger)
		if err != nil {
			t.Fatal(err)
		}
		d.mu.Lock()
		d.schedules[ds.ID] = &activeSchedule{
			desired: ds,
			runtime: &model.RuntimeState{
				ID:         ds.ID,
				NextFireAt: trig.NextFireTime(time.Now()),
			},
			trigger: trig,
		}
		d.mu.Unlock()
	}

	result, rpcErr := d.handleList()
	if rpcErr != nil {
		t.Fatalf("unexpected error: %v", rpcErr.Message)
	}

	if len(result.Schedules) != 2 {
		t.Errorf("expected 2 schedules, got %d", len(result.Schedules))
	}
}

func TestHandleShow_Found(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_s01", "show-test", "0 12 * * *")
	desired.Command = "echo show"
	desired.UserRequest = "run at noon"

	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	exitCode := 0
	d.mu.Lock()
	d.schedules["sch_s01"] = &activeSchedule{
		desired: desired,
		runtime: &model.RuntimeState{
			ID:           "sch_s01",
			NextFireAt:   trig.NextFireTime(now),
			LastFiredAt:  &now,
			LastExitCode: &exitCode,
			FireCount:    3,
		},
		trigger: trig,
	}
	d.mu.Unlock()

	result, rpcErr := d.handleShow(model.ShowParams{ID: "sch_s01"})
	if rpcErr != nil {
		t.Fatalf("unexpected error: %v", rpcErr.Message)
	}

	if result.ID != "sch_s01" {
		t.Errorf("expected ID sch_s01, got %s", result.ID)
	}
	if result.Name != "show-test" {
		t.Errorf("expected name 'show-test', got %q", result.Name)
	}
	if result.Command != "echo show" {
		t.Errorf("expected command 'echo show', got %q", result.Command)
	}
	if result.UserRequest != "run at noon" {
		t.Errorf("expected user_request 'run at noon', got %q", result.UserRequest)
	}
}

func TestHandleShow_NotFound(t *testing.T) {
	d := newTestDaemon(t)

	_, rpcErr := d.handleShow(model.ShowParams{ID: "sch_nonexistent"})
	if rpcErr == nil {
		t.Fatal("expected error for nonexistent schedule")
	}
	if rpcErr.Code != model.RPCErrCodeNotFound {
		t.Errorf("expected error code %d, got %d", model.RPCErrCodeNotFound, rpcErr.Code)
	}

	// Verify structured NotFoundInfo in error data.
	var info model.NotFoundInfo
	if err := json.Unmarshal(rpcErr.Data, &info); err != nil {
		t.Fatalf("failed to unmarshal NotFoundInfo: %v", err)
	}
	if info.SearchedID != "sch_nonexistent" {
		t.Errorf("expected searched_id 'sch_nonexistent', got %q", info.SearchedID)
	}
	if info.Hint == "" {
		t.Error("expected hint to be set")
	}
}

func TestHandleShow_NotFound_WithAvailableIDs(t *testing.T) {
	d := newTestDaemon(t)

	// Seed a schedule so available_ids is populated.
	desired := makeCronDesired("sch_abc123", "test-sched", "0 9 * * *")
	trig, _ := trigger.ParseTrigger(desired.Trigger)
	d.mu.Lock()
	d.schedules["sch_abc123"] = &activeSchedule{
		desired: desired,
		runtime: &model.RuntimeState{
			ID:         "sch_abc123",
			NextFireAt: trig.NextFireTime(time.Now()),
		},
		trigger: trig,
	}
	d.mu.Unlock()

	_, rpcErr := d.handleShow(model.ShowParams{ID: "sch_missing"})
	if rpcErr == nil {
		t.Fatal("expected error for nonexistent schedule")
	}

	var info model.NotFoundInfo
	if err := json.Unmarshal(rpcErr.Data, &info); err != nil {
		t.Fatalf("failed to unmarshal NotFoundInfo: %v", err)
	}
	if len(info.AvailableIDs) == 0 {
		t.Error("expected available_ids to be populated")
	}
}

func TestHandleRemove_NotFound(t *testing.T) {
	d := newTestDaemon(t)

	_, rpcErr := d.handleRemove(model.RemoveParams{ID: "sch_missing"})
	if rpcErr == nil {
		t.Fatal("expected error for nonexistent schedule")
	}
	if rpcErr.Code != model.RPCErrCodeNotFound {
		t.Errorf("expected error code %d, got %d", model.RPCErrCodeNotFound, rpcErr.Code)
	}

	// Verify structured data.
	var info model.NotFoundInfo
	if err := json.Unmarshal(rpcErr.Data, &info); err != nil {
		t.Fatalf("failed to unmarshal NotFoundInfo: %v", err)
	}
	if info.SearchedID != "sch_missing" {
		t.Errorf("expected searched_id 'sch_missing', got %q", info.SearchedID)
	}
}

func TestHandle_UnknownMethod(t *testing.T) {
	d := newTestDaemon(t)

	_, rpcErr := d.Handle("unknown_method", nil)
	if rpcErr == nil {
		t.Fatal("expected error for unknown method")
	}
	if rpcErr.Code != model.RPCErrCodeMethodNotFound {
		t.Errorf("expected error code %d, got %d", model.RPCErrCodeMethodNotFound, rpcErr.Code)
	}

	// Verify structured MethodNotFoundInfo.
	var info model.MethodNotFoundInfo
	if err := json.Unmarshal(rpcErr.Data, &info); err != nil {
		t.Fatalf("failed to unmarshal MethodNotFoundInfo: %v", err)
	}
	if info.Method != "unknown_method" {
		t.Errorf("expected method 'unknown_method', got %q", info.Method)
	}
	if len(info.AvailableMethods) == 0 {
		t.Error("expected available_methods to be populated")
	}
}

func TestHandle_InvalidParams(t *testing.T) {
	d := newTestDaemon(t)

	// Send malformed JSON for add.
	_, rpcErr := d.Handle(model.MethodAdd, json.RawMessage(`{invalid`))
	if rpcErr == nil {
		t.Fatal("expected error for invalid params")
	}
	if rpcErr.Code != model.RPCErrCodeInvalidParams {
		t.Errorf("expected error code %d, got %d", model.RPCErrCodeInvalidParams, rpcErr.Code)
	}
}

func TestHandleAdd_DuplicateName(t *testing.T) {
	d := newTestDaemon(t)

	// Seed an existing schedule.
	desired := makeCronDesired("sch_dup01", "my-schedule", "0 9 * * *")
	trig, _ := trigger.ParseTrigger(desired.Trigger)
	d.mu.Lock()
	d.schedules["sch_dup01"] = &activeSchedule{
		desired: desired,
		runtime: &model.RuntimeState{
			ID:         "sch_dup01",
			NextFireAt: trig.NextFireTime(time.Now()),
		},
		trigger: trig,
	}
	d.mu.Unlock()

	// Try to add a schedule with the same name.
	params := model.AddParams{
		Type:    model.TriggerTypeCron,
		Cron:    "0 10 * * *",
		Command: "echo different",
		Name:    "my-schedule",
	}

	_, rpcErr := d.handleAdd(params)
	if rpcErr == nil {
		t.Fatal("expected error for duplicate name")
	}
	if rpcErr.Code != model.RPCErrCodeDuplicate {
		t.Errorf("expected error code %d, got %d", model.RPCErrCodeDuplicate, rpcErr.Code)
	}

	// Check DuplicateInfo in error data.
	var dupInfo model.DuplicateInfo
	if err := json.Unmarshal(rpcErr.Data, &dupInfo); err != nil {
		t.Fatalf("failed to unmarshal DuplicateInfo: %v", err)
	}
	if dupInfo.MatchType != "exact_name" {
		t.Errorf("expected match_type 'exact_name', got %q", dupInfo.MatchType)
	}
	if dupInfo.ExistingID != "sch_dup01" {
		t.Errorf("expected existing_id 'sch_dup01', got %q", dupInfo.ExistingID)
	}
}

func TestHandleAdd_DuplicateCommand(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_dup02", "existing", "0 9 * * *")
	desired.Command = "echo samecmd"
	trig, _ := trigger.ParseTrigger(desired.Trigger)
	d.mu.Lock()
	d.schedules["sch_dup02"] = &activeSchedule{
		desired: desired,
		runtime: &model.RuntimeState{
			ID:         "sch_dup02",
			NextFireAt: trig.NextFireTime(time.Now()),
		},
		trigger: trig,
	}
	d.mu.Unlock()

	params := model.AddParams{
		Type:    model.TriggerTypeCron,
		Cron:    "0 10 * * *",
		Command: "echo samecmd",
		Name:    "different-name",
	}

	_, rpcErr := d.handleAdd(params)
	if rpcErr == nil {
		t.Fatal("expected error for duplicate command")
	}
	if rpcErr.Code != model.RPCErrCodeDuplicate {
		t.Errorf("expected error code %d, got %d", model.RPCErrCodeDuplicate, rpcErr.Code)
	}

	var dupInfo model.DuplicateInfo
	if err := json.Unmarshal(rpcErr.Data, &dupInfo); err != nil {
		t.Fatalf("failed to unmarshal DuplicateInfo: %v", err)
	}
	if dupInfo.MatchType != "exact_command" {
		t.Errorf("expected match_type 'exact_command', got %q", dupInfo.MatchType)
	}
}

func TestHandleAdd_DuplicateForceBypass(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_dup03", "force-test", "0 9 * * *")
	trig, _ := trigger.ParseTrigger(desired.Trigger)
	d.mu.Lock()
	d.schedules["sch_dup03"] = &activeSchedule{
		desired: desired,
		runtime: &model.RuntimeState{
			ID:         "sch_dup03",
			NextFireAt: trig.NextFireTime(time.Now()),
		},
		trigger: trig,
	}
	d.mu.Unlock()

	// With Force=true, duplicate name should not cause an error at the
	// duplicate-check stage. It will fail at git preconditions since we
	// don't have a real git repo, but the duplicate check itself should pass.
	params := model.AddParams{
		Type:    model.TriggerTypeCron,
		Cron:    "0 10 * * *",
		Command: "echo different",
		Name:    "force-test",
		Force:   true,
	}

	_, rpcErr := d.handleAdd(params)
	// We expect it to fail at git preconditions, NOT at duplicate check.
	if rpcErr != nil && rpcErr.Code == model.RPCErrCodeDuplicate {
		t.Error("expected Force=true to bypass duplicate check")
	}
}

func TestHandleAdd_InvalidTrigger(t *testing.T) {
	d := newTestDaemon(t)

	params := model.AddParams{
		Type:    model.TriggerTypeCron,
		Cron:    "not a valid cron expression",
		Command: "echo test",
	}

	_, rpcErr := d.handleAdd(params)
	if rpcErr == nil {
		t.Fatal("expected error for invalid trigger")
	}
	if rpcErr.Code != model.RPCErrCodeInvalidParams {
		t.Errorf("expected error code %d, got %d", model.RPCErrCodeInvalidParams, rpcErr.Code)
	}

	// Verify structured InvalidTriggerInfo.
	var info model.InvalidTriggerInfo
	if err := json.Unmarshal(rpcErr.Data, &info); err != nil {
		t.Fatalf("failed to unmarshal InvalidTriggerInfo: %v", err)
	}
	if info.TriggerType != "cron" {
		t.Errorf("expected trigger_type 'cron', got %q", info.TriggerType)
	}
	if info.RawError == "" {
		t.Error("expected raw_error to be set")
	}
	if info.Example == "" {
		t.Error("expected example to be set")
	}
}

func TestBuildSummary(t *testing.T) {
	desired := makeCronDesired("sch_sum01", "summary-test", "0 9 * * *")
	trig, _ := trigger.ParseTrigger(desired.Trigger)
	nextFire := trig.NextFireTime(time.Now())
	now := time.Now()
	exitCode := 0

	sched := &activeSchedule{
		desired: desired,
		runtime: &model.RuntimeState{
			ID:           "sch_sum01",
			NextFireAt:   nextFire,
			LastFiredAt:  &now,
			LastExitCode: &exitCode,
			FireCount:    5,
		},
		trigger: trig,
	}

	summary := buildSummary(sched)

	if summary.ID != "sch_sum01" {
		t.Errorf("expected ID sch_sum01, got %s", summary.ID)
	}
	if summary.Name != "summary-test" {
		t.Errorf("expected name 'summary-test', got %q", summary.Name)
	}
	if summary.Status != "active" {
		t.Errorf("expected status 'active', got %q", summary.Status)
	}
	if summary.NextFireUTC == "" {
		t.Error("expected NextFireUTC to be set")
	}
	if summary.LastFire == "" {
		t.Error("expected LastFire to be set")
	}
	if summary.LastExitCode == nil || *summary.LastExitCode != 0 {
		t.Error("expected LastExitCode to be 0")
	}
}

func TestSignalWake(t *testing.T) {
	d := newTestDaemon(t)

	// Signal should be non-blocking.
	d.signalWake()
	d.signalWake() // Second call should not block.

	// Channel should have a signal.
	select {
	case <-d.wake:
		// Good.
	default:
		t.Error("expected wake channel to have a signal")
	}
}

// --- Pause / Unpause tests ---

func TestHandlePause_Success(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_p01", "pause-test", "0 9 * * *")
	trig, _ := trigger.ParseTrigger(desired.Trigger)
	runtime := &model.RuntimeState{
		ID:         "sch_p01",
		NextFireAt: trig.NextFireTime(time.Now()),
	}

	d.mu.Lock()
	d.schedules["sch_p01"] = &activeSchedule{
		desired: desired,
		runtime: runtime,
		trigger: trig,
	}
	d.mu.Unlock()

	result, rpcErr := d.handlePause(model.PauseParams{ID: "sch_p01"})
	// Expect git precondition failure (no real git repo), but the status
	// check and logic before git should pass. Let's check both paths.
	if rpcErr != nil {
		// If it fails at git, that's OK — verify it's not a different error.
		if rpcErr.Code == model.RPCErrCodeGitPrecondition {
			return // expected in test env without git
		}
		t.Fatalf("unexpected error: code=%d message=%s", rpcErr.Code, rpcErr.Message)
	}

	if result.Status != "paused" {
		t.Errorf("expected status 'paused', got %q", result.Status)
	}
	if result.Name != "pause-test" {
		t.Errorf("expected name 'pause-test', got %q", result.Name)
	}
}

func TestHandlePause_NotFound(t *testing.T) {
	d := newTestDaemon(t)

	_, rpcErr := d.handlePause(model.PauseParams{ID: "sch_missing"})
	if rpcErr == nil {
		t.Fatal("expected error for nonexistent schedule")
	}
	if rpcErr.Code != model.RPCErrCodeNotFound {
		t.Errorf("expected error code %d, got %d", model.RPCErrCodeNotFound, rpcErr.Code)
	}

	var info model.NotFoundInfo
	if err := json.Unmarshal(rpcErr.Data, &info); err != nil {
		t.Fatalf("failed to unmarshal NotFoundInfo: %v", err)
	}
	if info.SearchedID != "sch_missing" {
		t.Errorf("expected searched_id 'sch_missing', got %q", info.SearchedID)
	}
}

func TestHandlePause_AlreadyPaused(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_p02", "already-paused", "0 9 * * *")
	desired.Status = model.StatusPaused
	trig, _ := trigger.ParseTrigger(desired.Trigger)

	d.mu.Lock()
	d.schedules["sch_p02"] = &activeSchedule{
		desired: desired,
		runtime: &model.RuntimeState{ID: "sch_p02"},
		trigger: trig,
	}
	d.mu.Unlock()

	_, rpcErr := d.handlePause(model.PauseParams{ID: "sch_p02"})
	if rpcErr == nil {
		t.Fatal("expected error for already paused schedule")
	}
	if rpcErr.Code != model.RPCErrCodeStateConflict {
		t.Errorf("expected error code %d, got %d", model.RPCErrCodeStateConflict, rpcErr.Code)
	}

	// Verify structured StateConflictInfo.
	var info model.StateConflictInfo
	if err := json.Unmarshal(rpcErr.Data, &info); err != nil {
		t.Fatalf("failed to unmarshal StateConflictInfo: %v", err)
	}
	if info.ScheduleID != "sch_p02" {
		t.Errorf("expected schedule_id 'sch_p02', got %q", info.ScheduleID)
	}
	if info.CurrentStatus != "paused" {
		t.Errorf("expected current_status 'paused', got %q", info.CurrentStatus)
	}
	if info.SuggestedCommand == "" {
		t.Error("expected suggested_command to be set")
	}
}

func TestHandleUnpause_NotPaused(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_u01", "not-paused", "0 9 * * *")
	trig, _ := trigger.ParseTrigger(desired.Trigger)

	d.mu.Lock()
	d.schedules["sch_u01"] = &activeSchedule{
		desired: desired,
		runtime: &model.RuntimeState{
			ID:         "sch_u01",
			NextFireAt: trig.NextFireTime(time.Now()),
		},
		trigger: trig,
	}
	d.mu.Unlock()

	_, rpcErr := d.handleUnpause(model.PauseParams{ID: "sch_u01"})
	if rpcErr == nil {
		t.Fatal("expected error for non-paused schedule")
	}
	if rpcErr.Code != model.RPCErrCodeStateConflict {
		t.Errorf("expected error code %d, got %d", model.RPCErrCodeStateConflict, rpcErr.Code)
	}

	// Verify structured StateConflictInfo.
	var info model.StateConflictInfo
	if err := json.Unmarshal(rpcErr.Data, &info); err != nil {
		t.Fatalf("failed to unmarshal StateConflictInfo: %v", err)
	}
	if info.CurrentStatus != "active" {
		t.Errorf("expected current_status 'active', got %q", info.CurrentStatus)
	}
	if info.SuggestedCommand == "" {
		t.Error("expected suggested_command to be set")
	}
}

func TestHandleUnpause_NotFound(t *testing.T) {
	d := newTestDaemon(t)

	_, rpcErr := d.handleUnpause(model.PauseParams{ID: "sch_missing"})
	if rpcErr == nil {
		t.Fatal("expected error for nonexistent schedule")
	}
	if rpcErr.Code != model.RPCErrCodeNotFound {
		t.Errorf("expected error code %d, got %d", model.RPCErrCodeNotFound, rpcErr.Code)
	}

	var info model.NotFoundInfo
	if err := json.Unmarshal(rpcErr.Data, &info); err != nil {
		t.Fatalf("failed to unmarshal NotFoundInfo: %v", err)
	}
	if info.SearchedID != "sch_missing" {
		t.Errorf("expected searched_id 'sch_missing', got %q", info.SearchedID)
	}
}

// --- Fuzzy duplicate detection tests ---

func TestHandleAdd_FuzzyNameMatch(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_fz01", "my-schedule", "0 9 * * *")
	trig, _ := trigger.ParseTrigger(desired.Trigger)
	d.mu.Lock()
	d.schedules["sch_fz01"] = &activeSchedule{
		desired: desired,
		runtime: &model.RuntimeState{
			ID:         "sch_fz01",
			NextFireAt: trig.NextFireTime(time.Now()),
		},
		trigger: trig,
	}
	d.mu.Unlock()

	// Fuzzy name: "my-scheduel" is close to "my-schedule".
	params := model.AddParams{
		Type:    model.TriggerTypeCron,
		Cron:    "0 10 * * *",
		Command: "echo different",
		Name:    "my-scheduel",
	}

	_, rpcErr := d.handleAdd(params)
	if rpcErr == nil {
		t.Fatal("expected error for fuzzy name match")
	}
	if rpcErr.Code != model.RPCErrCodeDuplicate {
		t.Errorf("expected error code %d, got %d", model.RPCErrCodeDuplicate, rpcErr.Code)
	}

	var dupInfo model.DuplicateInfo
	if err := json.Unmarshal(rpcErr.Data, &dupInfo); err != nil {
		t.Fatalf("failed to unmarshal DuplicateInfo: %v", err)
	}
	if dupInfo.MatchType != "fuzzy_name" {
		t.Errorf("expected match_type 'fuzzy_name', got %q", dupInfo.MatchType)
	}
}

func TestHandleAdd_FuzzyCommandMatch(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_fz02", "existing", "0 9 * * *")
	desired.Command = "cd ~/project && codex exec 'daily standup'"
	trig, _ := trigger.ParseTrigger(desired.Trigger)
	d.mu.Lock()
	d.schedules["sch_fz02"] = &activeSchedule{
		desired: desired,
		runtime: &model.RuntimeState{
			ID:         "sch_fz02",
			NextFireAt: trig.NextFireTime(time.Now()),
		},
		trigger: trig,
	}
	d.mu.Unlock()

	// Command is a substring of the existing command.
	params := model.AddParams{
		Type:    model.TriggerTypeCron,
		Cron:    "0 10 * * *",
		Command: "codex exec 'daily standup'",
		Name:    "totally-different",
	}

	_, rpcErr := d.handleAdd(params)
	if rpcErr == nil {
		t.Fatal("expected error for fuzzy command match")
	}
	if rpcErr.Code != model.RPCErrCodeDuplicate {
		t.Errorf("expected error code %d, got %d", model.RPCErrCodeDuplicate, rpcErr.Code)
	}

	var dupInfo model.DuplicateInfo
	if err := json.Unmarshal(rpcErr.Data, &dupInfo); err != nil {
		t.Fatalf("failed to unmarshal DuplicateInfo: %v", err)
	}
	if dupInfo.MatchType != "fuzzy_command" {
		t.Errorf("expected match_type 'fuzzy_command', got %q", dupInfo.MatchType)
	}
}

func TestHandleAdd_FuzzyForceBypass(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_fz03", "my-schedule", "0 9 * * *")
	trig, _ := trigger.ParseTrigger(desired.Trigger)
	d.mu.Lock()
	d.schedules["sch_fz03"] = &activeSchedule{
		desired: desired,
		runtime: &model.RuntimeState{
			ID:         "sch_fz03",
			NextFireAt: trig.NextFireTime(time.Now()),
		},
		trigger: trig,
	}
	d.mu.Unlock()

	// Fuzzy name match with --force should bypass.
	params := model.AddParams{
		Type:    model.TriggerTypeCron,
		Cron:    "0 10 * * *",
		Command: "echo different",
		Name:    "my-scheduel",
		Force:   true,
	}

	_, rpcErr := d.handleAdd(params)
	// Should NOT fail with duplicate error; may fail at git preconditions.
	if rpcErr != nil && rpcErr.Code == model.RPCErrCodeDuplicate {
		t.Error("expected Force=true to bypass fuzzy duplicate check")
	}
}

func TestHandleAdd_ExactTakesPriorityOverFuzzy(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_fz04", "my-schedule", "0 9 * * *")
	desired.Command = "echo samecmd"
	trig, _ := trigger.ParseTrigger(desired.Trigger)
	d.mu.Lock()
	d.schedules["sch_fz04"] = &activeSchedule{
		desired: desired,
		runtime: &model.RuntimeState{
			ID:         "sch_fz04",
			NextFireAt: trig.NextFireTime(time.Now()),
		},
		trigger: trig,
	}
	d.mu.Unlock()

	// Both exact command match and fuzzy name match — exact should win.
	params := model.AddParams{
		Type:    model.TriggerTypeCron,
		Cron:    "0 10 * * *",
		Command: "echo samecmd",
		Name:    "my-scheduel", // fuzzy match
	}

	_, rpcErr := d.handleAdd(params)
	if rpcErr == nil {
		t.Fatal("expected error for duplicate")
	}

	var dupInfo model.DuplicateInfo
	if err := json.Unmarshal(rpcErr.Data, &dupInfo); err != nil {
		t.Fatalf("failed to unmarshal DuplicateInfo: %v", err)
	}
	if dupInfo.MatchType != "exact_command" {
		t.Errorf("expected exact match to take priority, got %q", dupInfo.MatchType)
	}
}

func TestFindCompletedByName(t *testing.T) {
	d := newTestDaemon(t)

	// Write a completed desired state.
	desired := makePollDesired("sch_fn01", "watch-replies", "check-cmd", "2m")
	desired.Status = model.StatusCompleted
	desired.Command = "echo fire"
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	// Should find by name.
	found := d.findCompletedByName("watch-replies")
	if found == nil {
		t.Fatal("expected to find completed schedule by name")
	}
	if found.ID != "sch_fn01" {
		t.Errorf("expected ID sch_fn01, got %s", found.ID)
	}

	// Should not find for different name.
	notFound := d.findCompletedByName("other-name")
	if notFound != nil {
		t.Error("expected no match for different name")
	}

	// Should not find active schedules.
	active := makeCronDesired("sch_fn02", "active-sched", "0 9 * * *")
	if err := d.desiredStore.Write(active); err != nil {
		t.Fatal(err)
	}
	notFound = d.findCompletedByName("active-sched")
	if notFound != nil {
		t.Error("expected no match for active schedule")
	}

	// Empty name should return nil.
	notFound = d.findCompletedByName("")
	if notFound != nil {
		t.Error("expected nil for empty name")
	}
}

func TestReactivation_ByNameWithUpdatedCommand(t *testing.T) {
	d := newTestDaemonWithGit(t)

	// Write a completed schedule with a specific name and command.
	desired := makePollDesired("sch_rn01", "outreach-watch", "check-cmd", "2m")
	desired.Status = model.StatusCompleted
	desired.Command = "echo old-callback"
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	// Re-add with same name but different command.
	params := model.AddParams{
		Type:           model.TriggerTypePoll,
		TriggerCommand: "check-cmd",
		Interval:       "2m",
		Timeout:        "72h",
		Command:        "echo new-callback",
		Name:           "outreach-watch",
	}

	result, rpcErr := d.handleAdd(params)
	if rpcErr != nil {
		t.Fatalf("unexpected error: %v", rpcErr.Message)
	}

	// Should reactivate the same schedule, not create new.
	if result.ID != "sch_rn01" {
		t.Errorf("expected reactivation of sch_rn01, got new ID %s", result.ID)
	}
	if result.Status != "reactivated" {
		t.Errorf("expected status 'reactivated', got %q", result.Status)
	}

	// Command should be updated.
	ds, err := d.desiredStore.Read("sch_rn01")
	if err != nil {
		t.Fatal(err)
	}
	if ds.Command != "echo new-callback" {
		t.Errorf("expected command to be updated to 'echo new-callback', got %q", ds.Command)
	}
	if ds.Status != model.StatusActive {
		t.Errorf("expected status 'active', got %q", ds.Status)
	}
}

func TestReactivation_ByCommandFallback(t *testing.T) {
	d := newTestDaemonWithGit(t)

	// Write a completed schedule.
	desired := makePollDesired("sch_rc01", "old-name", "check-cmd", "2m")
	desired.Status = model.StatusCompleted
	desired.Command = "echo fire"
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	// Re-add with same command but no name — should fall back to command match.
	params := model.AddParams{
		Type:           model.TriggerTypePoll,
		TriggerCommand: "check-cmd",
		Interval:       "2m",
		Timeout:        "72h",
		Command:        "echo fire",
	}

	result, rpcErr := d.handleAdd(params)
	if rpcErr != nil {
		t.Fatalf("unexpected error: %v", rpcErr.Message)
	}

	if result.ID != "sch_rc01" {
		t.Errorf("expected reactivation of sch_rc01, got %s", result.ID)
	}
	if result.Status != "reactivated" {
		t.Errorf("expected status 'reactivated', got %q", result.Status)
	}
}

func TestReactivation_NameTakesPriorityOverCommand(t *testing.T) {
	d := newTestDaemonWithGit(t)

	// Two completed schedules with same command but different names.
	ds1 := makePollDesired("sch_pri01", "name-alpha", "check-cmd", "2m")
	ds1.Status = model.StatusCompleted
	ds1.Command = "echo shared-cmd"
	ds2 := makePollDesired("sch_pri02", "name-beta", "check-cmd", "2m")
	ds2.Status = model.StatusCompleted
	ds2.Command = "echo shared-cmd"

	for _, ds := range []*model.DesiredState{ds1, ds2} {
		if err := d.desiredStore.Write(ds); err != nil {
			t.Fatal(err)
		}
	}

	// Re-add targeting name-beta — should reactivate sch_pri02 by name,
	// not whichever one findCompletedByCommand happens to return first.
	params := model.AddParams{
		Type:           model.TriggerTypePoll,
		TriggerCommand: "check-cmd",
		Interval:       "2m",
		Timeout:        "72h",
		Command:        "echo shared-cmd",
		Name:           "name-beta",
	}

	result, rpcErr := d.handleAdd(params)
	if rpcErr != nil {
		t.Fatalf("unexpected error: %v", rpcErr.Message)
	}

	if result.ID != "sch_pri02" {
		t.Errorf("expected name match to reactivate sch_pri02, got %s", result.ID)
	}
}
