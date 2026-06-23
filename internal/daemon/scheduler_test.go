package daemon

import (
	"testing"
	"time"

	"github.com/fyang0507/sundial/internal/model"
	"github.com/fyang0507/sundial/internal/trigger"
)

// TestReconcileMissedFires_WakeGapBeyondGrace simulates a system-sleep wake gap:
// a cron schedule whose NextFireAt sat well in the past (beyond grace) while the
// monotonic timer was frozen. The wake-path reconciler must log the miss and
// advance NextFireAt to a future time so the run loop does not replay every
// occurrence that elapsed during sleep.
func TestReconcileMissedFires_WakeGapBeyondGrace(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_wake01", "wake-gap-beyond", "* * * * *") // every minute
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	// NextFireAt 30 minutes in the past — far beyond the 60s grace window, the
	// kind of gap a multi-minute system sleep produces.
	pastTime := time.Now().Add(-30 * time.Minute)
	runtime := &model.RuntimeState{
		ID:         "sch_wake01",
		NextFireAt: pastTime,
	}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}

	d.mu.Lock()
	d.schedules["sch_wake01"] = &activeSchedule{
		desired: desired,
		runtime: runtime,
		trigger: trig,
	}
	d.mu.Unlock()

	// Invoke the wake-path reconciler directly (it acquires d.mu itself).
	d.reconcileMissedFires()

	d.mu.RLock()
	sched := d.schedules["sch_wake01"]
	d.mu.RUnlock()

	// (a) a miss/miss_summary entry was logged.
	entries, err := d.runLogStore.Read("sch_wake01")
	if err != nil {
		t.Fatal(err)
	}
	hasMiss := false
	for _, e := range entries {
		if e.Type == model.LogTypeMiss || e.Type == model.LogTypeMissSummary {
			hasMiss = true
			break
		}
	}
	if !hasMiss {
		t.Error("expected a miss or miss_summary entry after wake-gap reconciliation")
	}

	// (b) NextFireAt advanced to a future time.
	if !sched.runtime.NextFireAt.After(time.Now()) {
		t.Errorf("expected NextFireAt advanced to the future, got %v", sched.runtime.NextFireAt)
	}

	// The reconciler must never execute the command itself.
	if sched.runtime.LastFiredAt != nil {
		t.Error("expected wake reconciler not to execute the command")
	}
}

// TestReconcileMissedFires_WithinGraceLeftInPast verifies that a within-grace
// past NextFireAt is LEFT in the past by the wake-path reconciler — neither
// advanced nor executed — so the run loop's fireDueSchedules pass fires it once.
func TestReconcileMissedFires_WithinGraceLeftInPast(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_wake02", "wake-gap-within", "0 9 * * *")
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}

	pastTime := time.Now().Add(-20 * time.Second) // within 60s grace
	runtime := &model.RuntimeState{
		ID:         "sch_wake02",
		NextFireAt: pastTime,
	}
	if err := d.runtimeStore.Write(runtime); err != nil {
		t.Fatal(err)
	}

	d.mu.Lock()
	d.schedules["sch_wake02"] = &activeSchedule{
		desired: desired,
		runtime: runtime,
		trigger: trig,
	}
	d.mu.Unlock()

	d.reconcileMissedFires()

	d.mu.RLock()
	sched := d.schedules["sch_wake02"]
	d.mu.RUnlock()

	// Left untouched in the past — fireDueSchedules would fire it.
	if !sched.runtime.NextFireAt.Equal(pastTime) {
		t.Errorf("expected within-grace NextFireAt left at %v, got %v", pastTime, sched.runtime.NextFireAt)
	}
	if sched.runtime.NextFireAt.After(time.Now()) {
		t.Error("expected within-grace NextFireAt to remain in the past")
	}

	// Not executed by the classifier.
	if sched.runtime.LastFiredAt != nil {
		t.Error("expected within-grace miss not to be executed by the reconciler")
	}

	// No miss entries for a within-grace catch-up.
	entries, err := d.runLogStore.Read("sch_wake02")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no miss entries for within-grace miss, got %d", len(entries))
	}
}
