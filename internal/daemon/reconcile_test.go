package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fyang0507/sundial/internal/gitops"
	"github.com/fyang0507/sundial/internal/model"
	"github.com/fyang0507/sundial/internal/store"
	"github.com/fyang0507/sundial/internal/trigger"
)

// newTestStores creates temp-dir-backed stores for testing.
func newTestStores(t *testing.T) (string, *store.DesiredStore, *store.RuntimeStore, *store.RunLogStore) {
	t.Helper()

	tmpDir := t.TempDir()
	dataRepoDir := filepath.Join(tmpDir, "data")
	stateDir := filepath.Join(tmpDir, "state")
	logsDir := filepath.Join(tmpDir, "logs")

	ds := store.NewDesiredStore(dataRepoDir)
	rs := store.NewRuntimeStore(stateDir)
	rl := store.NewRunLogStore(logsDir)

	if err := ds.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	if err := rs.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	if err := rl.EnsureDir(); err != nil {
		t.Fatal(err)
	}

	return dataRepoDir, ds, rs, rl
}

// newTestDaemon creates a Daemon with real stores in temp dirs, suitable for
// unit-testing reconciliation and handlers without IPC or git.
func newTestDaemon(t *testing.T) *Daemon {
	t.Helper()

	dataRepoDir, ds, rs, rl := newTestStores(t)

	d := &Daemon{
		cfg: &model.Config{
			DataRepo: dataRepoDir,
			State: model.StateConfig{
				Path:     "",
				LogsPath: "",
			},
		},
		desiredStore: ds,
		runtimeStore: rs,
		runLogStore:  rl,
		gitOps:       gitops.NewGitOps(dataRepoDir),
		schedules:    make(map[string]*activeSchedule),
		wake:         make(chan struct{}, 1),
		quit:         make(chan struct{}),
		done:         make(chan struct{}),
	}

	return d
}

// makeCronDesired creates a DesiredState with a cron trigger for testing.
func makeCronDesired(id, name, cron string) *model.DesiredState {
	return &model.DesiredState{
		ID:        id,
		Name:      name,
		CreatedAt: time.Now(),
		Trigger: model.TriggerConfig{
			Type: model.TriggerTypeCron,
			Cron: cron,
		},
		Command: "echo test",
		Status:  model.StatusActive,
	}
}

func TestReconcile_ActiveNoRuntime(t *testing.T) {
	d := newTestDaemon(t)

	// Write an active desired state.
	desired := makeCronDesired("sch_aaa111", "test-active", "0 9 * * *")
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	// Reconcile (not startup, to skip missed fires handling).
	if err := d.reconcile(false); err != nil {
		t.Fatal(err)
	}

	// Should have 1 active schedule.
	d.mu.RLock()
	defer d.mu.RUnlock()

	sched, ok := d.schedules["sch_aaa111"]
	if !ok {
		t.Fatal("expected schedule sch_aaa111 to be active after reconcile")
	}

	if sched.desired.Name != "test-active" {
		t.Errorf("expected name 'test-active', got %q", sched.desired.Name)
	}

	// Runtime should have been created.
	rs, err := d.runtimeStore.Read("sch_aaa111")
	if err != nil {
		t.Fatal(err)
	}
	if rs.NextFireAt.IsZero() {
		t.Error("expected NextFireAt to be set after reconcile")
	}
}

func TestReconcile_ActiveWithRuntime(t *testing.T) {
	d := newTestDaemon(t)

	// Write active desired and existing runtime.
	desired := makeCronDesired("sch_bbb222", "test-existing", "0 10 * * *")
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	runtime := &model.RuntimeState{
		ID:         "sch_bbb222",
		NextFireAt: time.Now().Add(2 * time.Hour),
		FireCount:  5,
	}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}

	if err := d.reconcile(false); err != nil {
		t.Fatal(err)
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	sched, ok := d.schedules["sch_bbb222"]
	if !ok {
		t.Fatal("expected schedule sch_bbb222 to be active after reconcile")
	}

	// NextFireAt should have been recomputed (advanced).
	if sched.runtime.NextFireAt.IsZero() {
		t.Error("expected NextFireAt to be recomputed")
	}
}

func TestReconcile_RemovedWithRuntime(t *testing.T) {
	d := newTestDaemon(t)

	// Write a "removed" desired state.
	desired := makeCronDesired("sch_ccc333", "test-removed", "0 11 * * *")
	desired.Status = model.StatusRemoved
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	// Write runtime state for it.
	runtime := &model.RuntimeState{
		ID:         "sch_ccc333",
		NextFireAt: time.Now().Add(time.Hour),
	}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}

	if err := d.reconcile(false); err != nil {
		t.Fatal(err)
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	if _, ok := d.schedules["sch_ccc333"]; ok {
		t.Error("expected removed schedule to not be in active schedules")
	}

	// Runtime state should have been deleted.
	_, err := d.runtimeStore.Read("sch_ccc333")
	if err == nil {
		t.Error("expected runtime state to be deleted for removed schedule")
	}
}

func TestReconcile_OrphanedRuntime(t *testing.T) {
	d := newTestDaemon(t)

	// Write only a runtime state, no desired state.
	runtime := &model.RuntimeState{
		ID:         "sch_ddd444",
		NextFireAt: time.Now().Add(time.Hour),
	}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}

	if err := d.reconcile(false); err != nil {
		t.Fatal(err)
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	if _, ok := d.schedules["sch_ddd444"]; ok {
		t.Error("expected orphaned schedule to not be in active schedules")
	}
}

func TestHandleMissedFires_WithinGrace(t *testing.T) {
	d := newTestDaemon(t)

	// Create a schedule with NextFireAt in the recent past (within grace).
	desired := makeCronDesired("sch_eee555", "test-grace", "0 9 * * *")
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	pastTime := time.Now().Add(-30 * time.Second) // 30s ago, within 60s grace
	runtime := &model.RuntimeState{
		ID:         "sch_eee555",
		NextFireAt: pastTime,
	}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}

	d.mu.Lock()
	d.schedules["sch_eee555"] = &activeSchedule{
		desired: desired,
		runtime: runtime,
		trigger: trig,
	}
	d.mu.Unlock()

	// handleMissedFires should execute the command (echo test).
	d.handleMissedFires()

	// Check that it fired: runtime should have a LastFiredAt and fire count.
	d.mu.RLock()
	sched := d.schedules["sch_eee555"]
	d.mu.RUnlock()

	if sched.runtime.LastFiredAt == nil {
		t.Error("expected LastFiredAt to be set after within-grace missed fire")
	}
	if sched.runtime.FireCount != 1 {
		t.Errorf("expected FireCount=1, got %d", sched.runtime.FireCount)
	}
}

func TestHandleMissedFires_BeyondGrace(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_fff666", "test-beyond-grace", "* * * * *") // every minute
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	// Set NextFireAt 5 minutes in the past (beyond 60s grace).
	pastTime := time.Now().Add(-5 * time.Minute)
	runtime := &model.RuntimeState{
		ID:         "sch_fff666",
		NextFireAt: pastTime,
	}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}

	d.mu.Lock()
	d.schedules["sch_fff666"] = &activeSchedule{
		desired: desired,
		runtime: runtime,
		trigger: trig,
	}
	d.mu.Unlock()

	d.handleMissedFires()

	// Should NOT have fired (no LastFiredAt change).
	d.mu.RLock()
	sched := d.schedules["sch_fff666"]
	d.mu.RUnlock()

	if sched.runtime.LastFiredAt != nil {
		t.Error("expected no fire for beyond-grace missed fire")
	}

	// Should have logged miss entries.
	entries, err := d.runLogStore.Read("sch_fff666")
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) == 0 {
		t.Fatal("expected miss log entries to be written")
	}

	// Check that at least one is a miss entry.
	hasMiss := false
	for _, e := range entries {
		if e.Type == model.LogTypeMiss || e.Type == model.LogTypeMissSummary {
			hasMiss = true
			break
		}
	}
	if !hasMiss {
		t.Error("expected miss or miss_summary entries in log")
	}
}

func TestHandleMissedFires_BackfillCap(t *testing.T) {
	d := newTestDaemon(t)

	// Every-minute cron, 60 minutes ago -> ~60 missed fires, should cap at 10+summary.
	desired := makeCronDesired("sch_ggg777", "test-cap", "* * * * *")
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	pastTime := time.Now().Add(-60 * time.Minute)
	runtime := &model.RuntimeState{
		ID:         "sch_ggg777",
		NextFireAt: pastTime,
	}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}

	d.mu.Lock()
	d.schedules["sch_ggg777"] = &activeSchedule{
		desired: desired,
		runtime: runtime,
		trigger: trig,
	}
	d.mu.Unlock()

	d.handleMissedFires()

	entries, err := d.runLogStore.Read("sch_ggg777")
	if err != nil {
		t.Fatal(err)
	}

	// Should have at most 11 entries: 10 miss + 1 miss_summary.
	if len(entries) > maxMissEntries+1 {
		t.Errorf("expected at most %d entries (10 miss + 1 summary), got %d",
			maxMissEntries+1, len(entries))
	}

	// Should have exactly 1 miss_summary.
	summaries := 0
	misses := 0
	for _, e := range entries {
		switch e.Type {
		case model.LogTypeMiss:
			misses++
		case model.LogTypeMissSummary:
			summaries++
			if e.Count < 1 {
				t.Errorf("expected miss_summary Count > 0, got %d", e.Count)
			}
		}
	}

	if misses != maxMissEntries {
		t.Errorf("expected %d miss entries, got %d", maxMissEntries, misses)
	}
	if summaries != 1 {
		t.Errorf("expected 1 miss_summary, got %d", summaries)
	}
}

func TestReconcile_MultipleSchedules(t *testing.T) {
	d := newTestDaemon(t)

	// Mix: one active (no runtime), one active (with runtime), one removed (with runtime).
	d1 := makeCronDesired("sch_m01", "active-new", "0 8 * * *")
	d2 := makeCronDesired("sch_m02", "active-existing", "0 9 * * *")
	d3 := makeCronDesired("sch_m03", "removed", "0 10 * * *")
	d3.Status = model.StatusRemoved

	for _, ds := range []*model.DesiredState{d1, d2, d3} {
		if err := d.desiredStore.Write(ds); err != nil {
			t.Fatal(err)
		}
	}

	// Runtime for d2 and d3.
	for _, id := range []string{"sch_m02", "sch_m03"} {
		rs := &model.RuntimeState{ID: id, NextFireAt: time.Now().Add(time.Hour)}
		if err := d.runtimeStore.Write(rs); err != nil {
			t.Fatal(err)
		}
	}

	// Orphaned runtime.
	orphanRS := &model.RuntimeState{ID: "sch_orphan", NextFireAt: time.Now().Add(time.Hour)}
	if err := d.runtimeStore.Write(orphanRS); err != nil {
		t.Fatal(err)
	}

	if err := d.reconcile(false); err != nil {
		t.Fatal(err)
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	// Should have exactly 2 active schedules.
	if len(d.schedules) != 2 {
		t.Errorf("expected 2 active schedules, got %d", len(d.schedules))
	}

	if _, ok := d.schedules["sch_m01"]; !ok {
		t.Error("expected sch_m01 (active-new) to be present")
	}
	if _, ok := d.schedules["sch_m02"]; !ok {
		t.Error("expected sch_m02 (active-existing) to be present")
	}
	if _, ok := d.schedules["sch_m03"]; ok {
		t.Error("expected sch_m03 (removed) to NOT be present")
	}
	if _, ok := d.schedules["sch_orphan"]; ok {
		t.Error("expected sch_orphan to NOT be present")
	}
}

// TestAdvanceAllSchedules verifies that NextFireAt is recomputed to a future time.
func TestAdvanceAllSchedules(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_adv01", "advance-test", "0 9 * * *")
	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	pastFire := time.Now().Add(-2 * time.Hour)
	runtime := &model.RuntimeState{
		ID:         "sch_adv01",
		NextFireAt: pastFire,
	}

	d.mu.Lock()
	d.schedules["sch_adv01"] = &activeSchedule{
		desired: desired,
		runtime: runtime,
		trigger: trig,
	}
	d.mu.Unlock()

	d.advanceAllSchedules()

	d.mu.RLock()
	sched := d.schedules["sch_adv01"]
	d.mu.RUnlock()

	if !sched.runtime.NextFireAt.After(time.Now()) {
		t.Errorf("expected NextFireAt to be in the future, got %v", sched.runtime.NextFireAt)
	}
}

// makePollDesired creates a DesiredState with a poll trigger for testing.
func makePollDesired(id, name, triggerCmd, interval string) *model.DesiredState {
	return &model.DesiredState{
		ID:        id,
		Name:      name,
		CreatedAt: time.Now(),
		Trigger: model.TriggerConfig{
			Type:           model.TriggerTypePoll,
			TriggerCommand: triggerCmd,
			Interval:       interval,
			Timeout:        "72h",
		},
		Command: "echo fired",
		Status:  model.StatusActive,
	}
}

func TestHandleMissedFires_PollSkipsMissHandling(t *testing.T) {
	d := newTestDaemon(t)

	desired := makePollDesired("sch_poll01", "poll-miss-test", "true", "2m")
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	// Set NextFireAt 5 minutes in the past (would normally log misses).
	pastTime := time.Now().Add(-5 * time.Minute)
	runtime := &model.RuntimeState{
		ID:         "sch_poll01",
		NextFireAt: pastTime,
	}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}

	d.mu.Lock()
	d.schedules["sch_poll01"] = &activeSchedule{
		desired: desired,
		runtime: runtime,
		trigger: trig,
	}
	d.mu.Unlock()

	d.handleMissedFires()

	// Should NOT have fired (no LastFiredAt).
	d.mu.RLock()
	sched := d.schedules["sch_poll01"]
	d.mu.RUnlock()

	if sched.runtime.LastFiredAt != nil {
		t.Error("poll trigger should not fire on missed checks")
	}

	// NextFireAt should have been advanced to the future.
	if !sched.runtime.NextFireAt.After(time.Now()) {
		t.Errorf("expected NextFireAt to be in the future, got %v", sched.runtime.NextFireAt)
	}

	// Should NOT have miss log entries.
	entries, err := d.runLogStore.Read("sch_poll01")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no miss log entries for poll trigger, got %d", len(entries))
	}
}

func TestReconcile_PollTrigger(t *testing.T) {
	d := newTestDaemon(t)

	desired := makePollDesired("sch_poll02", "poll-reconcile", "check-cmd", "2m")
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	if err := d.reconcile(false); err != nil {
		t.Fatal(err)
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	sched, ok := d.schedules["sch_poll02"]
	if !ok {
		t.Fatal("expected poll schedule to be active after reconcile")
	}

	if sched.runtime.NextFireAt.IsZero() {
		t.Error("expected NextFireAt to be set for poll trigger")
	}

	if !sched.runtime.NextFireAt.After(time.Now()) {
		t.Errorf("expected NextFireAt in the future, got %v", sched.runtime.NextFireAt)
	}
}

func TestAdvanceSchedule_Once(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_once01", "once-test", "0 9 * * *")
	desired.Once = true
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	runtime := &model.RuntimeState{
		ID:         "sch_once01",
		NextFireAt: time.Now().Add(time.Hour),
		FireCount:  1, // already fired once
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
	d.schedules["sch_once01"] = sched
	d.mu.Unlock()

	d.advanceSchedule(sched)

	// Schedule should be removed from active map.
	d.mu.RLock()
	_, ok := d.schedules["sch_once01"]
	d.mu.RUnlock()
	if ok {
		t.Error("expected completed once schedule to be removed from active schedules")
	}

	// Desired state should be "completed" with reason "triggered".
	ds, err := d.desiredStore.Read("sch_once01")
	if err != nil {
		t.Fatal(err)
	}
	if ds.Status != model.StatusCompleted {
		t.Errorf("expected desired status 'completed', got %q", ds.Status)
	}
	if ds.CompletionReason != model.CompletionTriggered {
		t.Errorf("expected completion reason 'triggered', got %q", ds.CompletionReason)
	}
}

func TestAdvanceSchedule_OnceCompletesDesiredState(t *testing.T) {
	d := newTestDaemon(t)

	desired := makePollDesired("sch_once_c01", "once-complete", "exit 0", "2m")
	desired.Once = true
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	runtime := &model.RuntimeState{
		ID:         "sch_once_c01",
		NextFireAt: time.Now().Add(time.Hour),
		FireCount:  1,
		LastFiredAt: &now,
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
	d.schedules["sch_once_c01"] = sched
	d.mu.Unlock()

	d.advanceSchedule(sched)

	// Desired state should be updated to "completed" with reason "triggered".
	ds, err := d.desiredStore.Read("sch_once_c01")
	if err != nil {
		t.Fatal(err)
	}
	if ds.Status != model.StatusCompleted {
		t.Errorf("expected desired status 'completed', got %q", ds.Status)
	}
	if ds.CompletionReason != model.CompletionTriggered {
		t.Errorf("expected completion reason 'triggered', got %q", ds.CompletionReason)
	}

	// Runtime state should have been deleted.
	_, err = d.runtimeStore.Read("sch_once_c01")
	if err == nil {
		t.Error("expected runtime state to be deleted after completion")
	}

	// Schedule should be removed from active map.
	d.mu.RLock()
	_, ok := d.schedules["sch_once_c01"]
	d.mu.RUnlock()
	if ok {
		t.Error("expected completed schedule to be removed from active schedules")
	}
}

func TestAdvanceSchedule_OnceNotYetFired(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_once02", "once-pending", "0 9 * * *")
	desired.Once = true
	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	runtime := &model.RuntimeState{
		ID:         "sch_once02",
		NextFireAt: time.Now().Add(time.Hour),
		FireCount:  0, // not fired yet
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
	d.schedules["sch_once02"] = sched
	d.mu.Unlock()

	d.advanceSchedule(sched)

	// NextFireAt should still be set (schedule hasn't fired yet).
	if sched.runtime.NextFireAt.IsZero() {
		t.Error("expected NextFireAt to be set for once schedule that hasn't fired yet")
	}
}

func TestReconcile_CompletedWithRuntime(t *testing.T) {
	d := newTestDaemon(t)

	// Write a "completed" desired state.
	desired := makeCronDesired("sch_comp01", "test-completed", "0 11 * * *")
	desired.Status = model.StatusCompleted
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	// Write runtime state for it.
	runtime := &model.RuntimeState{
		ID:         "sch_comp01",
		NextFireAt: time.Now().Add(time.Hour),
	}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}

	if err := d.reconcile(false); err != nil {
		t.Fatal(err)
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	if _, ok := d.schedules["sch_comp01"]; ok {
		t.Error("expected completed schedule to not be in active schedules")
	}

	// Runtime state should have been deleted.
	_, err := d.runtimeStore.Read("sch_comp01")
	if err == nil {
		t.Error("expected runtime state to be deleted for completed schedule")
	}
}

func TestFindCompletedByCommand(t *testing.T) {
	d := newTestDaemon(t)

	// Write a completed desired state.
	desired := makePollDesired("sch_find01", "find-test", "check-cmd", "2m")
	desired.Status = model.StatusCompleted
	desired.Command = "echo fire"
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	// Should find it.
	found := d.findCompletedByCommand("echo fire")
	if found == nil {
		t.Fatal("expected to find completed schedule")
	}
	if found.ID != "sch_find01" {
		t.Errorf("expected ID sch_find01, got %s", found.ID)
	}

	// Should not find for different command.
	notFound := d.findCompletedByCommand("echo other")
	if notFound != nil {
		t.Error("expected no match for different command")
	}

	// Should not find active schedules.
	active := makeCronDesired("sch_find02", "active-one", "0 9 * * *")
	active.Command = "echo active"
	if err := d.desiredStore.Write(active); err != nil {
		t.Fatal(err)
	}
	notFound = d.findCompletedByCommand("echo active")
	if notFound != nil {
		t.Error("expected no match for active schedule")
	}
}

func TestReconcile_PausedNoRuntime(t *testing.T) {
	d := newTestDaemon(t)

	// Write a paused desired state with no runtime.
	desired := makeCronDesired("sch_pause01", "test-paused", "0 9 * * *")
	desired.Status = model.StatusPaused
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	if err := d.reconcile(false); err != nil {
		t.Fatal(err)
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	sched, ok := d.schedules["sch_pause01"]
	if !ok {
		t.Fatal("expected paused schedule to be in active schedules map (for list/show)")
	}

	if sched.desired.Status != model.StatusPaused {
		t.Errorf("expected status 'paused', got %q", sched.desired.Status)
	}

	// NextFireAt should be zero (paused schedules don't fire).
	if !sched.runtime.NextFireAt.IsZero() {
		t.Errorf("expected zero NextFireAt for paused schedule, got %v", sched.runtime.NextFireAt)
	}
}

func TestReconcile_PausedWithRuntime(t *testing.T) {
	d := newTestDaemon(t)

	// Write a paused desired state with stale runtime.
	desired := makeCronDesired("sch_pause02", "test-paused-rt", "0 9 * * *")
	desired.Status = model.StatusPaused
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	runtime := &model.RuntimeState{
		ID:         "sch_pause02",
		NextFireAt: time.Now().Add(time.Hour), // stale non-zero value
		FireCount:  3,
	}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}

	if err := d.reconcile(false); err != nil {
		t.Fatal(err)
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	sched, ok := d.schedules["sch_pause02"]
	if !ok {
		t.Fatal("expected paused schedule to be in active schedules map")
	}

	// NextFireAt should be zeroed out during reconciliation.
	if !sched.runtime.NextFireAt.IsZero() {
		t.Errorf("expected zero NextFireAt for paused schedule, got %v", sched.runtime.NextFireAt)
	}

	// FireCount should be preserved.
	if sched.runtime.FireCount != 3 {
		t.Errorf("expected FireCount=3, got %d", sched.runtime.FireCount)
	}
}

func TestReconcile_MixedWithPaused(t *testing.T) {
	d := newTestDaemon(t)

	d1 := makeCronDesired("sch_mx01", "active-sched", "0 8 * * *")
	d2 := makeCronDesired("sch_mx02", "paused-sched", "0 9 * * *")
	d2.Status = model.StatusPaused
	d3 := makeCronDesired("sch_mx03", "removed-sched", "0 10 * * *")
	d3.Status = model.StatusRemoved

	for _, ds := range []*model.DesiredState{d1, d2, d3} {
		if err := d.desiredStore.Write(ds); err != nil {
			t.Fatal(err)
		}
	}

	if err := d.reconcile(false); err != nil {
		t.Fatal(err)
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	// Should have 2 schedules: active + paused.
	if len(d.schedules) != 2 {
		t.Errorf("expected 2 schedules (active + paused), got %d", len(d.schedules))
	}

	if _, ok := d.schedules["sch_mx01"]; !ok {
		t.Error("expected active schedule to be present")
	}
	if _, ok := d.schedules["sch_mx02"]; !ok {
		t.Error("expected paused schedule to be present")
	}
	if _, ok := d.schedules["sch_mx03"]; ok {
		t.Error("expected removed schedule to NOT be present")
	}

	// Active should have non-zero NextFireAt.
	if d.schedules["sch_mx01"].runtime.NextFireAt.IsZero() {
		t.Error("expected active schedule to have non-zero NextFireAt")
	}

	// Paused should have zero NextFireAt.
	if !d.schedules["sch_mx02"].runtime.NextFireAt.IsZero() {
		t.Error("expected paused schedule to have zero NextFireAt")
	}
}

func TestHandleMissedFires_PollTimeoutExpiredCompletesSchedule(t *testing.T) {
	d := newTestDaemon(t)

	// Create a poll schedule that was created 80h ago with a 72h timeout.
	desired := makePollDesired("sch_poll_to01", "poll-timeout-test", "true", "2m")
	desired.CreatedAt = time.Now().Add(-80 * time.Hour)
	desired.Trigger.Timeout = "72h"
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	pastTime := time.Now().Add(-5 * time.Minute)
	runtime := &model.RuntimeState{
		ID:         "sch_poll_to01",
		NextFireAt: pastTime,
	}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}

	d.mu.Lock()
	d.schedules["sch_poll_to01"] = &activeSchedule{
		desired: desired,
		runtime: runtime,
		trigger: trig,
	}
	d.mu.Unlock()

	d.handleMissedFires()

	// Schedule should be removed from active map (completed).
	d.mu.RLock()
	_, ok := d.schedules["sch_poll_to01"]
	d.mu.RUnlock()
	if ok {
		t.Error("expected timed-out poll schedule to be removed from active schedules")
	}

	// Desired state should be "completed" with reason "timeout".
	ds, err := d.desiredStore.Read("sch_poll_to01")
	if err != nil {
		t.Fatal(err)
	}
	if ds.Status != model.StatusCompleted {
		t.Errorf("expected desired status 'completed', got %q", ds.Status)
	}
	if ds.CompletionReason != model.CompletionTimeout {
		t.Errorf("expected completion reason 'timeout', got %q", ds.CompletionReason)
	}
}

func TestAdvanceSchedule_PollTimeoutCompletes(t *testing.T) {
	d := newTestDaemon(t)

	// Create a poll schedule whose timeout has expired.
	desired := makePollDesired("sch_poll_to02", "poll-timeout-advance", "true", "2m")
	desired.CreatedAt = time.Now().Add(-80 * time.Hour)
	desired.Trigger.Timeout = "72h"
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	runtime := &model.RuntimeState{
		ID:         "sch_poll_to02",
		NextFireAt: time.Now().Add(time.Hour),
		FireCount:  3,
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
	d.schedules["sch_poll_to02"] = sched
	d.mu.Unlock()

	d.advanceSchedule(sched)

	// Schedule should be completed with reason "timeout".
	d.mu.RLock()
	_, ok := d.schedules["sch_poll_to02"]
	d.mu.RUnlock()
	if ok {
		t.Error("expected timed-out poll schedule to be removed from active schedules")
	}

	ds, err := d.desiredStore.Read("sch_poll_to02")
	if err != nil {
		t.Fatal(err)
	}
	if ds.Status != model.StatusCompleted {
		t.Errorf("expected desired status 'completed', got %q", ds.Status)
	}
	if ds.CompletionReason != model.CompletionTimeout {
		t.Errorf("expected completion reason 'timeout', got %q", ds.CompletionReason)
	}
}

func TestAdvanceSchedule_PollNotTimedOutContinues(t *testing.T) {
	d := newTestDaemon(t)

	// Create a poll schedule with timeout still in the future.
	desired := makePollDesired("sch_poll_to03", "poll-no-timeout", "true", "2m")
	desired.CreatedAt = time.Now().Add(-1 * time.Hour) // created 1h ago
	desired.Trigger.Timeout = "72h"                     // 72h timeout — still active
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	runtime := &model.RuntimeState{
		ID:         "sch_poll_to03",
		NextFireAt: time.Now().Add(2 * time.Minute),
		FireCount:  1,
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
	d.schedules["sch_poll_to03"] = sched
	d.mu.Unlock()

	d.advanceSchedule(sched)

	// Schedule should still be active.
	d.mu.RLock()
	_, ok := d.schedules["sch_poll_to03"]
	d.mu.RUnlock()
	if !ok {
		t.Error("expected non-timed-out poll schedule to remain active")
	}

	// NextFireAt should be in the future.
	if !sched.runtime.NextFireAt.After(time.Now()) {
		t.Errorf("expected NextFireAt in the future, got %v", sched.runtime.NextFireAt)
	}
}

func makeAtDesired(id, name string, fireAt time.Time) *model.DesiredState {
	return &model.DesiredState{
		ID:        id,
		Name:      name,
		CreatedAt: time.Now(),
		Trigger: model.TriggerConfig{
			Type:   model.TriggerTypeAt,
			FireAt: fireAt.UTC().Format(time.RFC3339),
		},
		Command: "echo fired",
		Status:  model.StatusActive,
		Once:    true,
	}
}

func TestHandleMissedFires_AtBeyondGraceCompletes(t *testing.T) {
	d := newTestDaemon(t)

	// FireAt is 10 minutes in the past — beyond the 60s grace window.
	fireAt := time.Now().Add(-10 * time.Minute)
	desired := makeAtDesired("sch_at_miss1", "at-missed", fireAt)
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	runtime := &model.RuntimeState{
		ID:         "sch_at_miss1",
		NextFireAt: fireAt,
	}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}

	d.mu.Lock()
	d.schedules["sch_at_miss1"] = &activeSchedule{
		desired: desired,
		runtime: runtime,
		trigger: trig,
	}
	d.mu.Unlock()

	d.handleMissedFires()

	// Schedule should be removed from the active map.
	d.mu.RLock()
	_, ok := d.schedules["sch_at_miss1"]
	d.mu.RUnlock()
	if ok {
		t.Error("expected missed at schedule to be removed from active schedules")
	}

	// Desired state should be completed with reason "missed".
	ds, err := d.desiredStore.Read("sch_at_miss1")
	if err != nil {
		t.Fatal(err)
	}
	if ds.Status != model.StatusCompleted {
		t.Errorf("expected desired status 'completed', got %q", ds.Status)
	}
	if ds.CompletionReason != model.CompletionMissed {
		t.Errorf("expected completion reason 'missed', got %q", ds.CompletionReason)
	}

	// One miss entry should be recorded.
	entries, err := d.runLogStore.Read("sch_at_miss1")
	if err != nil {
		t.Fatal(err)
	}
	misses := 0
	for _, e := range entries {
		if e.Type == model.LogTypeMiss {
			misses++
		}
	}
	if misses != 1 {
		t.Errorf("expected 1 miss log entry, got %d", misses)
	}
}

// Ensure the schedules dir exists before running any file-based test.
func init() {
	// Temp dirs handle this in each test, nothing needed globally.
	_ = os.MkdirAll(os.TempDir(), 0755)
}
