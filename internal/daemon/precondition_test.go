package daemon

import (
	"testing"
	"time"

	"github.com/fyang0507/sundial/internal/model"
	"github.com/fyang0507/sundial/internal/trigger"
)

// addSchedule wires a desired state into a test daemon's active map with a fresh
// runtime whose NextFireAt is now, returning the activeSchedule. It mirrors the
// boilerplate the existing executor tests repeat inline.
func addSchedule(t *testing.T, d *Daemon, desired *model.DesiredState) *activeSchedule {
	t.Helper()
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
	return sched
}

// TestExecute_PreconditionPassesFires verifies the precondition gate lets a fire
// through when the precondition exits 0: the main command runs and FireCount
// increments, exactly as if no precondition were set.
func TestExecute_PreconditionPassesFires(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_pre_pass01", "pre-pass", "0 9 * * *")
	desired.Command = "echo fired"
	desired.Precondition = "exit 0"
	sched := addSchedule(t, d, desired)

	outcome := d.execute(sched)

	if !outcome.Executed {
		t.Error("expected Executed=true when precondition passes")
	}
	if outcome.Deferred {
		t.Error("expected Deferred=false when precondition passes")
	}
	if sched.runtime.FireCount != 1 {
		t.Errorf("expected FireCount=1, got %d", sched.runtime.FireCount)
	}
}

// TestExecute_PreconditionFailsDefers verifies a non-zero precondition defers the
// fire: the command does NOT run, FireCount is unchanged, and execute reports
// Deferred so the caller arranges a retry.
func TestExecute_PreconditionFailsDefers(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_pre_fail01", "pre-fail", "0 9 * * *")
	desired.Command = "echo should-not-run"
	desired.Precondition = "exit 1"
	sched := addSchedule(t, d, desired)

	outcome := d.execute(sched)

	if outcome.Executed {
		t.Error("expected Executed=false when precondition fails")
	}
	if !outcome.Deferred {
		t.Error("expected Deferred=true when precondition fails")
	}
	if sched.runtime.FireCount != 0 {
		t.Errorf("expected FireCount unchanged (0), got %d", sched.runtime.FireCount)
	}
	if sched.runtime.LastFiredAt != nil {
		t.Error("expected LastFiredAt nil when precondition defers")
	}
}

// TestDeferPrecondition_FirstDeferralSchedulesBackoff verifies the first deferral
// writes a 'deferred' log entry, advances NextFireAt by the first backoff value
// (1m default), and sets PreconditionAttempts to 1 without advancing the regular
// schedule.
func TestDeferPrecondition_FirstDeferralSchedulesBackoff(t *testing.T) {
	d := newTestDaemon(t)

	// Next regular fire is far away (9am cron) so the backoff retry never crosses it.
	desired := makeCronDesired("sch_pre_backoff01", "pre-backoff", "0 9 * * *")
	desired.Precondition = "exit 1"
	sched := addSchedule(t, d, desired)

	intendedFire := time.Now()
	before := time.Now()
	d.deferPrecondition(sched, intendedFire)
	after := time.Now()

	if sched.runtime.PreconditionAttempts != 1 {
		t.Errorf("expected PreconditionAttempts=1, got %d", sched.runtime.PreconditionAttempts)
	}
	if sched.runtime.PreconditionFirstDeferredAt == nil {
		t.Fatal("expected PreconditionFirstDeferredAt to be set")
	}
	// NextFireAt should be ~now + 1m (the first default backoff entry).
	wantLo := before.Add(1 * time.Minute)
	wantHi := after.Add(1 * time.Minute)
	if sched.runtime.NextFireAt.Before(wantLo) || sched.runtime.NextFireAt.After(wantHi) {
		t.Errorf("expected NextFireAt ~now+1m, got %s (window %s..%s)",
			sched.runtime.NextFireAt, wantLo, wantHi)
	}

	entries, err := d.runLogStore.Read("sch_pre_backoff01")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 run log entry, got %d", len(entries))
	}
	if entries[0].Type != model.LogTypeDeferred {
		t.Errorf("expected log type %q, got %q", model.LogTypeDeferred, entries[0].Type)
	}
}

// TestDeferPrecondition_SecondDeferralUsesSecondBackoff verifies a consecutive
// deferral indexes the next backoff entry (5m default) and increments attempts.
func TestDeferPrecondition_SecondDeferralUsesSecondBackoff(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_pre_backoff02", "pre-backoff2", "0 9 * * *")
	desired.Precondition = "exit 1"
	sched := addSchedule(t, d, desired)

	// Simulate one prior deferral.
	first := time.Now().Add(-1 * time.Minute)
	sched.runtime.PreconditionAttempts = 1
	sched.runtime.PreconditionFirstDeferredAt = &first

	before := time.Now()
	d.deferPrecondition(sched, time.Now())
	after := time.Now()

	if sched.runtime.PreconditionAttempts != 2 {
		t.Errorf("expected PreconditionAttempts=2, got %d", sched.runtime.PreconditionAttempts)
	}
	// Second backoff entry is 5m.
	wantLo := before.Add(5 * time.Minute)
	wantHi := after.Add(5 * time.Minute)
	if sched.runtime.NextFireAt.Before(wantLo) || sched.runtime.NextFireAt.After(wantHi) {
		t.Errorf("expected NextFireAt ~now+5m, got %s (window %s..%s)",
			sched.runtime.NextFireAt, wantLo, wantHi)
	}
}

// TestDeferPrecondition_GiveUpCrossesNextRegularFire verifies that when the next
// backoff retry would land at/after the next regular fire, the daemon gives up:
// it logs a miss with reason "precondition not met", resets the retry state, and
// advances NextFireAt to the next regular fire instead of retrying.
func TestDeferPrecondition_GiveUpCrossesNextRegularFire(t *testing.T) {
	d := newTestDaemon(t)

	// Fires every minute; the next regular fire is <= 60s away, so a default 1m
	// backoff retry lands at/after it and we give up.
	desired := makeCronDesired("sch_pre_giveup01", "pre-giveup", "* * * * *")
	desired.Precondition = "exit 1"
	sched := addSchedule(t, d, desired)

	intendedFire := time.Now()
	d.deferPrecondition(sched, intendedFire)

	if sched.runtime.PreconditionAttempts != 0 {
		t.Errorf("expected PreconditionAttempts reset to 0 on give-up, got %d", sched.runtime.PreconditionAttempts)
	}
	if sched.runtime.PreconditionFirstDeferredAt != nil {
		t.Error("expected PreconditionFirstDeferredAt reset to nil on give-up")
	}
	if sched.runtime.NextFireAt.IsZero() || !sched.runtime.NextFireAt.After(time.Now()) {
		t.Errorf("expected NextFireAt advanced to a future regular fire, got %s", sched.runtime.NextFireAt)
	}

	entries, err := d.runLogStore.Read("sch_pre_giveup01")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 miss entry, got %d", len(entries))
	}
	if entries[0].Type != model.LogTypeMiss {
		t.Errorf("expected log type %q, got %q", model.LogTypeMiss, entries[0].Type)
	}
	if entries[0].Reason != "precondition not met" {
		t.Errorf("expected reason 'precondition not met', got %q", entries[0].Reason)
	}
	if entries[0].ScheduledFor == nil || !entries[0].ScheduledFor.Equal(intendedFire) {
		t.Errorf("expected scheduled_for == intended fire %s, got %v", intendedFire, entries[0].ScheduledFor)
	}
}

// TestDeferPrecondition_AtGiveUpAfterMaxElapsedCompletes verifies that a one-off
// `at` schedule whose precondition never passes within the max-elapsed budget is
// completed with reason "missed" (there is no next regular fire to advance to).
func TestDeferPrecondition_AtGiveUpAfterMaxElapsedCompletes(t *testing.T) {
	d := newTestDaemonWithGit(t)

	// `at` already in the past so trigger.NextFireTime(now) is zero (no recurrence).
	fireAt := time.Now().Add(-1 * time.Minute)
	desired := makeAtDesired("sch_pre_at_giveup", "pre-at-giveup", fireAt)
	desired.Precondition = "exit 1"
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}
	sched := addSchedule(t, d, desired)

	// First deferral happened well outside the elapsed budget so we give up now.
	first := time.Now().Add(-3 * time.Hour) // beyond the 2h default at-budget
	sched.runtime.PreconditionAttempts = 3
	sched.runtime.PreconditionFirstDeferredAt = &first

	d.deferPrecondition(sched, fireAt)

	// Schedule should be removed from the active map (completed).
	d.mu.RLock()
	_, stillActive := d.schedules["sch_pre_at_giveup"]
	d.mu.RUnlock()
	if stillActive {
		t.Error("expected `at` schedule to be removed from active map after give-up")
	}

	ds, err := d.desiredStore.Read("sch_pre_at_giveup")
	if err != nil {
		t.Fatal(err)
	}
	if ds.Status != model.StatusCompleted {
		t.Errorf("expected status completed, got %q", ds.Status)
	}
	if ds.CompletionReason != model.CompletionMissed {
		t.Errorf("expected completion reason %q, got %q", model.CompletionMissed, ds.CompletionReason)
	}

	entries, err := d.runLogStore.Read("sch_pre_at_giveup")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Type != model.LogTypeMiss {
		t.Fatalf("expected 1 miss entry, got %+v", entries)
	}
	if entries[0].Reason != "precondition not met" {
		t.Errorf("expected reason 'precondition not met', got %q", entries[0].Reason)
	}
}

// TestExecute_PreconditionResetsStateOnPass verifies that a passing precondition
// clears any pending retry bookkeeping left by earlier deferrals, so the next
// deferral starts a fresh backoff sequence.
func TestExecute_PreconditionResetsStateOnPass(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_pre_reset01", "pre-reset", "0 9 * * *")
	desired.Command = "echo fired"
	desired.Precondition = "exit 0"
	sched := addSchedule(t, d, desired)

	// Seed stale retry state from a prior deferral sequence.
	first := time.Now().Add(-10 * time.Minute)
	sched.runtime.PreconditionAttempts = 2
	sched.runtime.PreconditionFirstDeferredAt = &first

	if !d.execute(sched).Executed {
		t.Fatal("expected execute to fire when precondition passes")
	}
	if sched.runtime.PreconditionAttempts != 0 {
		t.Errorf("expected PreconditionAttempts reset to 0, got %d", sched.runtime.PreconditionAttempts)
	}
	if sched.runtime.PreconditionFirstDeferredAt != nil {
		t.Error("expected PreconditionFirstDeferredAt reset to nil after pass")
	}
}

// TestEffectivePreconditionBackoff_PerScheduleOverride verifies a per-schedule
// backoff override is honored, and a malformed one falls back to the daemon
// default.
func TestEffectivePreconditionBackoff_PerScheduleOverride(t *testing.T) {
	d := newTestDaemon(t)

	desired := makeCronDesired("sch_pre_ovr01", "pre-ovr", "0 9 * * *")
	desired.PreconditionBackoff = []string{"10s", "20s"}
	sched := &activeSchedule{desired: desired, runtime: &model.RuntimeState{ID: desired.ID}}

	got := d.effectivePreconditionBackoff(sched)
	want := []time.Duration{10 * time.Second, 20 * time.Second}
	if len(got) != len(want) {
		t.Fatalf("expected %d entries, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d: expected %s, got %s", i, want[i], got[i])
		}
	}

	// Malformed override -> daemon/package default (1m first).
	desired.PreconditionBackoff = []string{"not-a-duration"}
	got = d.effectivePreconditionBackoff(sched)
	if got[0] != time.Minute {
		t.Errorf("expected fallback default first backoff 1m, got %s", got[0])
	}
}

// TestValidateExecutionParams covers the daemon-layer guard that rejects
// malformed duration flags from a direct RPC client (parity with the CLI), so
// invalid values can't be persisted to surface only as a WARN at fire time.
func TestValidateExecutionParams(t *testing.T) {
	cases := []struct {
		name    string
		p       model.AddParams
		wantErr bool
	}{
		{"empty is valid", model.AddParams{}, false},
		{"valid exec_timeout", model.AddParams{ExecTimeout: "30m"}, false},
		{"bad exec_timeout", model.AddParams{ExecTimeout: "nope"}, true},
		{"zero exec_timeout", model.AddParams{ExecTimeout: "0s"}, true},
		{"exec_timeout with detach", model.AddParams{ExecTimeout: "30m", Detach: true}, true},
		{"valid precondition backoff", model.AddParams{Precondition: "exit 0", PreconditionBackoff: []string{"1m", "5m"}}, false},
		{"bad backoff entry", model.AddParams{Precondition: "exit 0", PreconditionBackoff: []string{"1m", "x"}}, true},
		{"backoff without precondition", model.AddParams{PreconditionBackoff: []string{"1m"}}, true},
		{"valid max-elapsed", model.AddParams{Precondition: "exit 0", PreconditionMaxElapsed: "6h"}, false},
		{"bad max-elapsed", model.AddParams{Precondition: "exit 0", PreconditionMaxElapsed: "soon"}, true},
		{"max-elapsed without precondition", model.AddParams{PreconditionMaxElapsed: "6h"}, true},
		// Argv-array form of the command fields.
		{"array precondition satisfies backoff requirement", model.AddParams{PreconditionArgs: []string{"/my check.sh"}, PreconditionBackoff: []string{"1m"}}, false},
		{"array precondition satisfies max-elapsed requirement", model.AddParams{PreconditionArgs: []string{"/my check.sh"}, PreconditionMaxElapsed: "6h"}, false},
		{"command and command_args mutually exclusive", model.AddParams{Command: "echo hi", CommandArgs: []string{"/a b.sh"}}, true},
		{"precondition and precondition_args mutually exclusive", model.AddParams{Precondition: "exit 0", PreconditionArgs: []string{"/a b.sh"}}, true},
		{"trigger_command and trigger_command_args mutually exclusive", model.AddParams{TriggerCommand: "exit 0", TriggerCommandArgs: []string{"/a b.sh"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateExecutionParams(tc.p)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateExecutionParams(%+v): got err=%v, wantErr=%v", tc.p, err, tc.wantErr)
			}
		})
	}
}

// TestHandleMissedFires_PendingPreconditionRetrySurvivesWakeGap is the regression
// test for the cross-feature blocker: a schedule sitting in precondition backoff
// stores a FUTURE NextFireAt, which becomes past if the machine sleeps through the
// retry (or the daemon restarts). The missed-fire classifier must NOT treat that as
// a missed fire — doing so would log a spurious miss, discard the in-flight backoff
// (leaving a stale attempt count/anchor), or prematurely complete an `at`. Instead
// it must leave the state intact so the next fireDueSchedules pass re-checks the
// precondition and re-applies the correct backoff/give-up.
func TestHandleMissedFires_PendingPreconditionRetrySurvivesWakeGap(t *testing.T) {
	t.Run("recurring cron retry is not misclassified", func(t *testing.T) {
		d := newTestDaemon(t)

		desired := makeCronDesired("sch_pre_wake01", "pre-wake", "0 9 * * *")
		desired.Precondition = "exit 1"
		sched := addSchedule(t, d, desired)

		// Simulate an in-flight backoff retry whose retry time is now well in the
		// past (beyond the 60s grace), as if we slept through it.
		anchor := time.Now().Add(-30 * time.Minute)
		intended := anchor
		sched.runtime.PreconditionAttempts = 2
		sched.runtime.PreconditionFirstDeferredAt = &anchor
		sched.runtime.PreconditionIntendedFire = &intended
		pastRetry := time.Now().Add(-10 * time.Minute)
		sched.runtime.NextFireAt = pastRetry

		d.mu.Lock()
		d.handleMissedFires()
		d.mu.Unlock()

		// Retry state preserved.
		if sched.runtime.PreconditionAttempts != 2 {
			t.Errorf("expected attempts preserved at 2, got %d", sched.runtime.PreconditionAttempts)
		}
		if sched.runtime.PreconditionFirstDeferredAt == nil {
			t.Error("expected first-deferred anchor preserved")
		}
		// NextFireAt left in the past so the next tick re-checks the precondition.
		if !sched.runtime.NextFireAt.Equal(pastRetry) {
			t.Errorf("expected NextFireAt left at the past retry time %s, got %s", pastRetry, sched.runtime.NextFireAt)
		}
		// No spurious miss entries.
		entries, err := d.runLogStore.Read("sch_pre_wake01")
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Errorf("expected no run-log entries, got %d (%+v)", len(entries), entries)
		}
	})

	t.Run("one-off at retry is not prematurely completed", func(t *testing.T) {
		d := newTestDaemon(t)

		// FireAt in the past; the at has a max-elapsed budget that has NOT expired.
		desired := makeAtDesired("sch_pre_wake02", "pre-wake-at", time.Now().Add(-30*time.Minute))
		desired.Precondition = "exit 1"
		desired.PreconditionMaxElapsed = "6h"
		if err := d.desiredStore.Write(desired); err != nil {
			t.Fatal(err)
		}
		sched := addSchedule(t, d, desired)

		anchor := time.Now().Add(-20 * time.Minute)
		intended := time.Now().Add(-30 * time.Minute)
		sched.runtime.PreconditionAttempts = 1
		sched.runtime.PreconditionFirstDeferredAt = &anchor
		sched.runtime.PreconditionIntendedFire = &intended
		sched.runtime.NextFireAt = time.Now().Add(-5 * time.Minute)

		d.mu.Lock()
		d.handleMissedFires()
		d.mu.Unlock()

		// The schedule must still be active (not completed/removed) and retain state.
		d.mu.RLock()
		_, stillActive := d.schedules["sch_pre_wake02"]
		d.mu.RUnlock()
		if !stillActive {
			t.Error("expected the at schedule to remain active (not prematurely completed)")
		}
		if sched.runtime.PreconditionFirstDeferredAt == nil {
			t.Error("expected first-deferred anchor preserved on the at schedule")
		}
		entries, err := d.runLogStore.Read("sch_pre_wake02")
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Errorf("expected no miss entries for the deferred at, got %d", len(entries))
		}
	})
}
