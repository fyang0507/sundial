package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/fyang0507/sundial/internal/model"
	"github.com/fyang0507/sundial/internal/trigger"
)

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
	if rpcErr.Code != model.RPCErrCodeInvalidParams {
		t.Errorf("expected error code %d, got %d", model.RPCErrCodeInvalidParams, rpcErr.Code)
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
	if rpcErr.Code != model.RPCErrCodeInvalidParams {
		t.Errorf("expected error code %d, got %d", model.RPCErrCodeInvalidParams, rpcErr.Code)
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
