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

// Ensure the schedules dir exists before running any file-based test.
func init() {
	// Temp dirs handle this in each test, nothing needed globally.
	_ = os.MkdirAll(os.TempDir(), 0755)
}
