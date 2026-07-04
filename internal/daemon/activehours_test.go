package daemon

import (
	"fmt"
	"testing"
	"time"

	"github.com/fyang0507/sundial/internal/model"
	"github.com/fyang0507/sundial/internal/trigger"
)

// windowExcludingNow builds a global ActiveHours window that opens ~2h from now
// and therefore does not contain the current instant, so the active-hours gate
// is guaranteed to suppress. Timezone is empty (follow local) so it matches the
// wall clock the daemon reads.
func windowExcludingNow() *model.ActiveHours {
	m := clockMinuteNow()
	start := (m + 120) % 1440
	end := (m + 180) % 1440
	return &model.ActiveHours{Start: hhmm(start), End: hhmm(end)}
}

// windowIncludingNow builds a window centered on the current instant.
func windowIncludingNow() *model.ActiveHours {
	m := clockMinuteNow()
	start := (m + 1440 - 60) % 1440
	end := (m + 60) % 1440
	return &model.ActiveHours{Start: hhmm(start), End: hhmm(end)}
}

func clockMinuteNow() int {
	now := time.Now()
	return now.Hour()*60 + now.Minute()
}

func hhmm(min int) string { return fmt.Sprintf("%02d:%02d", min/60, min%60) }

// buildSched constructs an activeSchedule, resolving its window against the
// daemon's global active-hours config (d.activeHours) — set that before calling.
func buildSched(t *testing.T, d *Daemon, desired *model.DesiredState) *activeSchedule {
	t.Helper()
	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}
	return &activeSchedule{
		desired: desired,
		runtime: &model.RuntimeState{ID: desired.ID, NextFireAt: time.Now()},
		trigger: trig,
		window:  d.buildWindow(desired),
	}
}

func TestExecute_ActiveHoursGateSuppresses(t *testing.T) {
	d := newTestDaemon(t)
	d.activeHours = windowExcludingNow()

	desired := makeCronDesired("sch_ah_gate", "gate", "* * * * *")
	desired.Command = "exit 0"
	sched := buildSched(t, d, desired)

	outcome := d.execute(sched)

	if !outcome.Suppressed {
		t.Fatalf("expected Suppressed outcome, got %+v", outcome)
	}
	if outcome.Executed {
		t.Error("command should not have executed outside active hours")
	}
	if sched.runtime.FireCount != 0 {
		t.Errorf("FireCount should stay 0, got %d", sched.runtime.FireCount)
	}
	if sched.runtime.LastFiredAt != nil {
		t.Error("LastFiredAt should stay nil when suppressed")
	}
}

func TestExecute_ActiveHoursInsideWindowFires(t *testing.T) {
	d := newTestDaemon(t)
	d.activeHours = windowIncludingNow()

	desired := makeCronDesired("sch_ah_inside", "inside", "* * * * *")
	desired.Command = "exit 0"
	sched := buildSched(t, d, desired)

	outcome := d.execute(sched)

	if outcome.Suppressed {
		t.Fatalf("did not expect suppression inside window, got %+v", outcome)
	}
	if !outcome.Executed || sched.runtime.FireCount != 1 {
		t.Errorf("expected a normal fire, got outcome=%+v FireCount=%d", outcome, sched.runtime.FireCount)
	}
}

func TestExecute_IgnoreActiveHoursFires(t *testing.T) {
	d := newTestDaemon(t)
	d.activeHours = windowExcludingNow() // global window would suppress...

	desired := makeCronDesired("sch_ah_ignore", "ignore", "* * * * *")
	desired.Command = "exit 0"
	desired.IgnoreActiveHours = true // ...but this schedule opts out.
	sched := buildSched(t, d, desired)
	if sched.window != nil {
		t.Fatal("opted-out schedule should have no window")
	}

	outcome := d.execute(sched)

	if outcome.Suppressed {
		t.Fatalf("opted-out schedule must not be suppressed, got %+v", outcome)
	}
	if !outcome.Executed || sched.runtime.FireCount != 1 {
		t.Errorf("expected a normal fire for opted-out schedule, got outcome=%+v FireCount=%d", outcome, sched.runtime.FireCount)
	}
}

func TestExecute_ActiveHoursGate_Poll(t *testing.T) {
	d := newTestDaemon(t)
	d.activeHours = windowExcludingNow()

	// A poll whose trigger check would pass (exit 0) must still be suppressed
	// outside active hours — the gate is ahead of the poll check.
	desired := makePollDesired("sch_ah_poll", "poll-gate", "exit 0", "2m")
	desired.Command = "exit 0"
	sched := buildSched(t, d, desired)

	outcome := d.execute(sched)

	if !outcome.Suppressed {
		t.Fatalf("expected poll suppression outside active hours, got %+v", outcome)
	}
	if sched.runtime.CheckCount != 0 {
		t.Errorf("poll check should not run when suppressed, CheckCount=%d", sched.runtime.CheckCount)
	}
}

func TestExecute_ActiveHoursGate_At(t *testing.T) {
	d := newTestDaemon(t)
	d.activeHours = windowExcludingNow()

	desired := makeAtDesired("sch_ah_at", "at-gate", time.Now())
	sched := buildSched(t, d, desired)

	outcome := d.execute(sched)

	if !outcome.Suppressed {
		t.Fatalf("expected at suppression outside active hours, got %+v", outcome)
	}
	if sched.runtime.FireCount != 0 {
		t.Errorf("at FireCount should stay 0 when suppressed, got %d", sched.runtime.FireCount)
	}
}

func TestSuppressFire_DefersToWindowOpenAndLogs(t *testing.T) {
	d := newTestDaemon(t)
	d.activeHours = windowExcludingNow()

	desired := makeCronDesired("sch_ah_suppress", "suppress", "* * * * *")
	sched := buildSched(t, d, desired)
	if err := d.runtimeStore.Write(sched.runtime); err != nil {
		t.Fatal(err)
	}

	intended := time.Now()
	d.suppressFire(sched, intended)

	// NextFireAt must be the next window opening and must fall inside the window.
	if !sched.window.Contains(sched.runtime.NextFireAt) {
		t.Errorf("deferred NextFireAt %s should be inside the window", sched.runtime.NextFireAt)
	}
	wantOpen := sched.window.NextOpen(intended)
	if !sched.runtime.NextFireAt.Equal(wantOpen) {
		t.Errorf("NextFireAt = %s, want window open %s", sched.runtime.NextFireAt, wantOpen)
	}

	// A distinct "suppressed" run-log entry must have been recorded.
	entries, err := d.runLogStore.Read(desired.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found *model.RunLogEntry
	for _, e := range entries {
		if e.Type == model.LogTypeSuppressed {
			found = e
		}
	}
	if found == nil {
		t.Fatalf("expected a %q run-log entry, got %+v", model.LogTypeSuppressed, entries)
	}
	if found.ScheduledFor == nil || !found.ScheduledFor.Equal(intended) {
		t.Errorf("suppressed entry scheduled_for = %v, want %s", found.ScheduledFor, intended)
	}
}

func TestAdvanceSchedule_ClampsAndLogsSuppressed(t *testing.T) {
	d := newTestDaemon(t)
	d.activeHours = windowExcludingNow()

	// Every-minute cron: the next slot is ~1min out, well outside a window that
	// opens ~2h from now, so advance must clamp forward and log a suppression.
	desired := makeCronDesired("sch_ah_advance", "advance", "* * * * *")
	sched := buildSched(t, d, desired)
	if err := d.runtimeStore.Write(sched.runtime); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	d.schedules[desired.ID] = sched
	d.mu.Unlock()

	d.advanceSchedule(sched)

	if !sched.window.Contains(sched.runtime.NextFireAt) {
		t.Errorf("advanced NextFireAt %s should be clamped inside the window", sched.runtime.NextFireAt)
	}
	entries, err := d.runLogStore.Read(desired.ID)
	if err != nil {
		t.Fatal(err)
	}
	hasSuppressed := false
	for _, e := range entries {
		if e.Type == model.LogTypeSuppressed {
			hasSuppressed = true
		}
	}
	if !hasSuppressed {
		t.Errorf("expected a suppressed entry after clamping advance, got %+v", entries)
	}
}

func TestReconcile_ClampsInitialFireIntoWindow(t *testing.T) {
	d := newTestDaemon(t)
	d.activeHours = windowExcludingNow()

	desired := makeCronDesired("sch_ah_recon", "recon", "* * * * *")
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}

	if err := d.reconcile(false); err != nil {
		t.Fatal(err)
	}

	rs, err := d.runtimeStore.Read(desired.ID)
	if err != nil {
		t.Fatal(err)
	}
	w := d.buildWindow(desired)
	if !w.Contains(rs.NextFireAt) {
		t.Errorf("reconcile NextFireAt %s should be clamped into the window", rs.NextFireAt)
	}
}

func TestHandleAdd_ObeysGlobalWindowAndClamps(t *testing.T) {
	d := newTestDaemonWithGit(t)
	d.activeHours = windowExcludingNow()

	p := model.AddParams{
		Type:    model.TriggerTypeCron,
		Cron:    "* * * * *",
		Command: "echo hi",
		Name:    "ah-add",
	}

	res, rpcErr := d.handleAdd(p)
	if rpcErr != nil {
		t.Fatalf("handleAdd failed: %v", rpcErr.Message)
	}

	d.mu.RLock()
	sched := d.schedules[res.ID]
	d.mu.RUnlock()
	if sched.window == nil {
		t.Fatal("schedule should obey the global window")
	}
	if !sched.window.Contains(sched.runtime.NextFireAt) {
		t.Errorf("first fire %s should be clamped into the window", sched.runtime.NextFireAt)
	}

	// show exposes the window and the suppressed flag (we are outside the window).
	show, rpcErr := d.handleShow(model.ShowParams{ID: res.ID})
	if rpcErr != nil {
		t.Fatalf("handleShow failed: %v", rpcErr.Message)
	}
	if show.ActiveHours == "" {
		t.Error("expected show to expose active_hours")
	}
	if !show.Suppressed {
		t.Error("expected show to report suppressed=true outside the window")
	}
	if show.IgnoreActiveHours {
		t.Error("schedule should not be marked ignore_active_hours")
	}
}

func TestHandleAdd_IgnoreActiveHoursExempts(t *testing.T) {
	d := newTestDaemonWithGit(t)
	d.activeHours = windowExcludingNow()

	p := model.AddParams{
		Type:              model.TriggerTypeCron,
		Cron:              "0 3 * * *",
		Command:           "backup",
		Name:              "nightly-backup",
		IgnoreActiveHours: true,
	}

	res, rpcErr := d.handleAdd(p)
	if rpcErr != nil {
		t.Fatalf("handleAdd failed: %v", rpcErr.Message)
	}

	d.mu.RLock()
	sched := d.schedules[res.ID]
	d.mu.RUnlock()
	if !sched.desired.IgnoreActiveHours {
		t.Error("expected IgnoreActiveHours to be persisted")
	}
	if sched.window != nil {
		t.Error("opted-out schedule should carry no window")
	}

	show, rpcErr := d.handleShow(model.ShowParams{ID: res.ID})
	if rpcErr != nil {
		t.Fatalf("handleShow failed: %v", rpcErr.Message)
	}
	if !show.IgnoreActiveHours {
		t.Error("expected show to report ignore_active_hours=true")
	}
	if show.Suppressed {
		t.Error("opted-out schedule must never be suppressed")
	}
}

func TestHandleAdd_RefreshTogglesIgnoreActiveHours(t *testing.T) {
	d := newTestDaemonWithGit(t)
	d.activeHours = windowExcludingNow()

	// Create an obeying schedule.
	base := model.AddParams{
		Type: model.TriggerTypeCron, Cron: "* * * * *",
		Command: "echo hi", Name: "toggle",
	}
	var res model.AddResult
	if r, e := d.handleAdd(base); e != nil {
		t.Fatalf("add: %v", e.Message)
	} else {
		res = *r
	}
	d.mu.RLock()
	sched := d.schedules[res.ID]
	d.mu.RUnlock()
	if sched.window == nil {
		t.Fatal("initially should obey the window")
	}

	// Refresh with --ignore-active-hours: must flip to exempt and drop the window.
	refresh := base
	refresh.Refresh = true
	refresh.IgnoreActiveHours = true
	if _, e := d.handleAdd(refresh); e != nil {
		t.Fatalf("refresh: %v", e.Message)
	}
	d.mu.RLock()
	sched = d.schedules[res.ID]
	d.mu.RUnlock()
	if !sched.desired.IgnoreActiveHours {
		t.Error("refresh should have set IgnoreActiveHours")
	}
	if sched.window != nil {
		t.Error("refresh to opted-out should rebuild window to nil")
	}
}

func TestHandleAdd_AtOutsideWindowDefersToOpening(t *testing.T) {
	d := newTestDaemonWithGit(t)
	// Window opens ~2h from now; schedule an `at` for ~30min from now (inside the
	// closed period) — it must be deferred to the window opening, not fire early.
	d.activeHours = windowExcludingNow()
	fireAt := time.Now().Add(30 * time.Minute)

	p := model.AddParams{
		Type:    model.TriggerTypeAt,
		FireAt:  fireAt.UTC().Format(time.RFC3339),
		Command: "echo one-shot",
		Name:    "one-shot",
	}
	var res model.AddResult
	if rpcErr := func() *model.RPCError { r, e := d.handleAdd(p); res = *r; return e }(); rpcErr != nil {
		t.Fatalf("handleAdd: %v", rpcErr.Message)
	}

	d.mu.RLock()
	sched := d.schedules[res.ID]
	d.mu.RUnlock()
	if sched.window == nil {
		t.Fatal("at schedule should obey the global window")
	}
	if !sched.window.Contains(sched.runtime.NextFireAt) {
		t.Errorf("at NextFireAt %s should be clamped into the window", sched.runtime.NextFireAt)
	}
	if !sched.runtime.NextFireAt.After(fireAt) {
		t.Errorf("at fire %s should be deferred past its raw FireAt %s", sched.runtime.NextFireAt, fireAt)
	}
}

func TestReconcile_ReclampsFuturePreWindowFire(t *testing.T) {
	// Simulates enabling active_hours (or restarting in it) with a schedule whose
	// persisted NextFireAt predates the window and falls outside it: startup
	// reconcile must re-clamp the future fire into the window.
	d := newTestDaemon(t)
	d.activeHours = windowExcludingNow()

	desired := makeCronDesired("sch_reclamp", "reclamp", "* * * * *")
	if err := d.desiredStore.Write(desired); err != nil {
		t.Fatal(err)
	}
	// A future fire ~1min out (raw every-minute slot), outside the window that
	// opens ~2h from now — mimics a NextFireAt computed before the window existed.
	preWindow := time.Now().Add(1 * time.Minute)
	if err := d.runtimeStore.Write(&model.RuntimeState{ID: desired.ID, NextFireAt: preWindow}); err != nil {
		t.Fatal(err)
	}

	if err := d.reconcile(true); err != nil { // startup reconcile
		t.Fatal(err)
	}

	rs, err := d.runtimeStore.Read(desired.ID)
	if err != nil {
		t.Fatal(err)
	}
	w := d.buildWindow(desired)
	if !w.Contains(rs.NextFireAt) {
		t.Errorf("startup should re-clamp pre-window fire into the window; NextFireAt=%s", rs.NextFireAt)
	}
	if !rs.NextFireAt.After(preWindow) {
		t.Errorf("re-clamped fire %s should be later than the pre-window slot %s", rs.NextFireAt, preWindow)
	}
}

func TestHandleSetActiveHours_SetClearReclamps(t *testing.T) {
	d := newTestDaemon(t)

	// A schedule with no window: NextFireAt is its raw next slot.
	desired := makeCronDesired("sch_set", "set", "* * * * *")
	sched := buildSched(t, d, desired)
	if sched.window != nil {
		t.Fatal("no window expected before set")
	}
	d.mu.Lock()
	d.schedules[desired.ID] = sched
	d.mu.Unlock()
	if err := d.runtimeStore.Write(sched.runtime); err != nil {
		t.Fatal(err)
	}

	// Set a window that excludes now: the schedule must gain a window, get
	// re-clamped into it, and the daemon must report the window.
	ah := windowExcludingNow()
	res, rerr := d.handleSetActiveHours(model.SetActiveHoursParams{Window: ah.Start + "-" + ah.End})
	if rerr != nil {
		t.Fatalf("set: %v", rerr.Message)
	}
	if res.Reclamped < 1 {
		t.Errorf("expected >=1 re-clamped, got %d", res.Reclamped)
	}
	if sched.window == nil || !sched.window.Contains(sched.runtime.NextFireAt) {
		t.Errorf("schedule should be windowed and clamped in-window; window=%v next=%s", sched.window, sched.runtime.NextFireAt)
	}
	if d.getActiveHours() == nil {
		t.Error("daemon activeHours should be set")
	}

	// Persisted so a restart keeps it.
	if st, err := d.settingsStore.Read(); err != nil {
		t.Fatal(err)
	} else if st.ActiveHours == nil {
		t.Error("settings should persist the window")
	}

	// Clear: window drops, schedule re-clamps to raw slot.
	if _, rerr := d.handleSetActiveHours(model.SetActiveHoursParams{Clear: true}); rerr != nil {
		t.Fatalf("clear: %v", rerr.Message)
	}
	if d.getActiveHours() != nil {
		t.Error("daemon activeHours should be nil after clear")
	}
	if sched.window != nil {
		t.Error("schedule window should be nil after clear")
	}
	if st, err := d.settingsStore.Read(); err != nil {
		t.Fatal(err)
	} else if st.ActiveHours != nil {
		t.Error("settings should persist the cleared (nil) window")
	}
}

func TestHandleSetActiveHours_InvalidRejected(t *testing.T) {
	d := newTestDaemon(t)
	if _, rerr := d.handleSetActiveHours(model.SetActiveHoursParams{Window: "8am-10pm"}); rerr == nil {
		t.Fatal("expected error for malformed window")
	}
	if _, rerr := d.handleSetActiveHours(model.SetActiveHoursParams{Window: ""}); rerr == nil {
		t.Fatal("expected error for empty window without --clear")
	}
}

// setZone directly overrides the daemon's tracked local zone (bypassing OS
// detection) so a test can simulate a machine that traveled between zones.
func setZone(t *testing.T, d *Daemon, name string) {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load zone %s: %v", name, err)
	}
	d.localZoneMu.Lock()
	d.localZoneName = name
	d.localZone = loc
	d.localZoneMu.Unlock()
}

func TestReconcileTimezone_FollowLocalWindowTravels(t *testing.T) {
	d := newTestDaemon(t)
	setZone(t, d, "America/New_York")
	// A follow-local global window (empty Timezone) — the default that tracks the host.
	d.activeHours = &model.ActiveHours{Start: "08:00", End: "22:00"}

	desired := makeCronDesired("sch_tz_follow", "follow", "* * * * *")
	sched := buildSched(t, d, desired)
	if sched.window.Loc.String() != "America/New_York" {
		t.Fatalf("window should start in NY, got %s", sched.window.Loc)
	}
	d.mu.Lock()
	d.schedules[desired.ID] = sched
	d.mu.Unlock()
	if err := d.runtimeStore.Write(sched.runtime); err != nil {
		t.Fatal(err)
	}

	// Fly NYC -> SFO: the machine zone changes, then reconcileTimezone runs.
	setZone(t, d, "America/Los_Angeles")
	d.reconcileTimezone()

	if sched.window.Loc.String() != "America/Los_Angeles" {
		t.Errorf("follow-local window should have moved to LA, got %s", sched.window.Loc)
	}
	if !sched.window.Contains(sched.runtime.NextFireAt) {
		t.Errorf("next fire %s should be eligible in the new LA window", sched.runtime.NextFireAt)
	}
}

func TestReconcileTimezone_PinnedWindowDoesNotTravel(t *testing.T) {
	d := newTestDaemon(t)
	setZone(t, d, "America/New_York")
	// A pinned global window must NOT follow the machine.
	d.activeHours = &model.ActiveHours{Start: "08:00", End: "22:00", Timezone: "America/New_York"}

	desired := makeCronDesired("sch_tz_pinned", "pinned", "* * * * *")
	sched := buildSched(t, d, desired)
	d.mu.Lock()
	d.schedules[desired.ID] = sched
	d.mu.Unlock()

	setZone(t, d, "America/Los_Angeles")
	d.reconcileTimezone()

	if sched.window.Loc.String() != "America/New_York" {
		t.Errorf("pinned window should stay in NY, got %s", sched.window.Loc)
	}
}

func TestMaybeReconcileTimezone_DetectsEnvChange(t *testing.T) {
	d := newTestDaemon(t)
	setZone(t, d, "America/New_York")
	d.activeHours = &model.ActiveHours{Start: "08:00", End: "22:00"}

	// localtz.Name prefers $TZ, so setting it simulates the OS reporting a new
	// zone. maybeReconcileTimezone should notice and adopt it.
	t.Setenv("TZ", "America/Los_Angeles")
	d.maybeReconcileTimezone()

	d.localZoneMu.Lock()
	got := d.localZoneName
	d.localZoneMu.Unlock()
	if got != "America/Los_Angeles" {
		t.Errorf("expected daemon to adopt LA after TZ change, got %s", got)
	}
}
