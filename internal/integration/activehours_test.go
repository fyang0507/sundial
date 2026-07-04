package integration

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/fyang0507/sundial/internal/daemon"
	"github.com/fyang0507/sundial/internal/ipc"
	"github.com/fyang0507/sundial/internal/model"
	"github.com/fyang0507/sundial/internal/store"
)

// setupActiveHoursDaemon builds a daemon whose global config carries the given
// active-hours window (empty tz => follow local). When start is false the caller
// must call env.daemon.Start() after pre-seeding state.
func setupActiveHoursDaemon(t *testing.T, activeHours string, start bool) *testEnv {
	t.Helper()

	dataDir, err := os.MkdirTemp("", "sundial-ah-data-*")
	if err != nil {
		t.Fatalf("create data dir: %v", err)
	}
	initGitRepo(t, dataDir)
	stateDir, err := os.MkdirTemp("", "sundial-ah-state-*")
	if err != nil {
		t.Fatalf("create state dir: %v", err)
	}
	logsDir, err := os.MkdirTemp("", "sundial-ah-logs-*")
	if err != nil {
		t.Fatalf("create logs dir: %v", err)
	}
	socketPath := fmt.Sprintf("/tmp/sundial-ah-%s.sock", randomHex(8))

	cfg := &model.Config{
		DataRepo: dataDir,
		Daemon: model.DaemonConfig{
			SocketPath: socketPath,
			LogLevel:   "info",
		},
		State: model.StateConfig{Path: stateDir, LogsPath: logsDir},
	}

	// Pre-seed the active-hours window into local settings so daemon.New loads it
	// at startup (mirrors a persisted `sundial set-active-hours`).
	if activeHours != "" {
		ah, err := model.ParseActiveHours(activeHours, "")
		if err != nil {
			t.Fatalf("parse active hours %q: %v", activeHours, err)
		}
		ss := store.NewSettingsStore(stateDir)
		if err := ss.EnsureDir(); err != nil {
			t.Fatalf("ensure settings dir: %v", err)
		}
		if err := ss.Write(&model.Settings{ActiveHours: ah}); err != nil {
			t.Fatalf("write settings: %v", err)
		}
	}

	d, err := daemon.New(cfg)
	if err != nil {
		t.Fatalf("create daemon: %v", err)
	}

	env := &testEnv{daemon: d, client: ipc.NewClient(socketPath), cfg: cfg, dataDir: dataDir}
	t.Cleanup(func() {
		d.Stop()
		os.Remove(socketPath)
		os.RemoveAll(dataDir)
		os.RemoveAll(stateDir)
		os.RemoveAll(logsDir)
	})

	if start {
		if err := d.Start(); err != nil {
			t.Fatalf("start daemon: %v", err)
		}
		waitForSocket(t, env.client)
	}
	return env
}

// hhmmFromNow returns a "HH:MM" clock string offsetMinutes from the current
// local wall clock (wrapping past midnight).
func hhmmFromNow(offsetMinutes int) string {
	now := time.Now()
	m := ((now.Hour()*60+now.Minute())+offsetMinutes)%1440 + 1440
	m %= 1440
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

// windowExcludingNow returns a window that opens ~2h from now (excludes now).
func windowExcludingNow() string { return hhmmFromNow(120) + "-" + hhmmFromNow(180) }

// windowIncludingNow returns a window spanning now-60m .. now+60m.
func windowIncludingNow() string { return hhmmFromNow(-60) + "-" + hhmmFromNow(60) }

func readRunLog(t *testing.T, env *testEnv, id string) []*model.RunLogEntry {
	t.Helper()
	entries, err := store.NewRunLogStore(env.cfg.State.LogsPath).Read(id)
	if err != nil {
		t.Fatalf("read run log %s: %v", id, err)
	}
	return entries
}

func countType(entries []*model.RunLogEntry, typ model.RunLogType) int {
	n := 0
	for _, e := range entries {
		if e.Type == typ {
			n++
		}
	}
	return n
}

// TestActiveHours_GlobalWindowSuppressesAndClamps drives the full RPC path: a
// schedule added while OUTSIDE the global window must be clamped to the next
// opening and reported as suppressed, without firing.
func TestActiveHours_GlobalWindowSuppressesAndClamps(t *testing.T) {
	env := setupActiveHoursDaemon(t, windowExcludingNow(), true)

	p := model.AddParams{
		Type:    model.TriggerTypeCron,
		Cron:    "* * * * *", // every minute — would fire within 60s absent a window
		Command: "echo hi",
		Name:    "gated",
	}
	var res model.AddResult
	if err := env.client.Call(model.MethodAdd, p, &res); err != nil {
		t.Fatalf("add: %v", err)
	}

	var show model.ShowResult
	if err := env.client.Call(model.MethodShow, model.ShowParams{ID: res.ID}, &show); err != nil {
		t.Fatalf("show: %v", err)
	}
	if !show.Suppressed {
		t.Errorf("expected suppressed=true outside the window, got show=%+v", show)
	}
	if show.ActiveHours == "" {
		t.Error("expected show to report the active_hours window")
	}
	// The next fire must be clamped WELL into the future (window opens ~2h out),
	// not the raw every-minute slot (<60s away).
	next, err := time.Parse(time.RFC3339, show.NextFireUTC)
	if err != nil {
		t.Fatalf("parse next_fire_utc %q: %v", show.NextFireUTC, err)
	}
	if until := time.Until(next); until < 30*time.Minute {
		t.Errorf("next fire %s is only %s away; expected it clamped to the window opening (~2h)", next, until)
	}

	// Give the run loop a moment; it must NOT fire while suppressed.
	time.Sleep(1500 * time.Millisecond)
	entries := readRunLog(t, env, res.ID)
	if got := countType(entries, model.LogTypeFire); got != 0 {
		t.Errorf("expected 0 fire entries while suppressed, got %d (%+v)", got, entries)
	}
}

// TestActiveHours_OptOutIgnoresWindow verifies --ignore-active-hours exempts a
// schedule: it is not clamped, not suppressed, and reports ignore_active_hours.
func TestActiveHours_OptOutIgnoresWindow(t *testing.T) {
	env := setupActiveHoursDaemon(t, windowExcludingNow(), true)

	p := model.AddParams{
		Type:              model.TriggerTypeCron,
		Cron:              "* * * * *",
		Command:           "echo backup",
		Name:              "nightly",
		IgnoreActiveHours: true,
	}
	var res model.AddResult
	if err := env.client.Call(model.MethodAdd, p, &res); err != nil {
		t.Fatalf("add: %v", err)
	}

	var show model.ShowResult
	if err := env.client.Call(model.MethodShow, model.ShowParams{ID: res.ID}, &show); err != nil {
		t.Fatalf("show: %v", err)
	}
	if !show.IgnoreActiveHours {
		t.Error("expected ignore_active_hours=true")
	}
	if show.Suppressed {
		t.Error("opted-out schedule must not be suppressed")
	}
	if show.ActiveHours != "" {
		t.Errorf("opted-out schedule should report no window, got %q", show.ActiveHours)
	}
	// Not clamped: the next fire is the raw every-minute slot (< ~2min away).
	next, err := time.Parse(time.RFC3339, show.NextFireUTC)
	if err != nil {
		t.Fatalf("parse next_fire_utc %q: %v", show.NextFireUTC, err)
	}
	if until := time.Until(next); until > 2*time.Minute {
		t.Errorf("opted-out next fire %s is %s away; expected the raw slot (<2min), not a clamped window", next, until)
	}
}

// TestSetActiveHours_AppliesLiveAndReclamps proves the set_active_hours RPC:
// starting with NO window, a schedule fires soon; after setting a window that
// excludes now, the schedule is re-clamped into the window (live, no restart),
// and clearing it restores the raw slot.
func TestSetActiveHours_AppliesLiveAndReclamps(t *testing.T) {
	env := setupActiveHoursDaemon(t, "", true) // no window initially

	p := model.AddParams{
		Type: model.TriggerTypeCron, Cron: "* * * * *",
		Command: "echo hi", Name: "live",
	}
	var res model.AddResult
	if err := env.client.Call(model.MethodAdd, p, &res); err != nil {
		t.Fatalf("add: %v", err)
	}
	// No window yet: add response carries no active_hours reminder, next fire is
	// the raw slot (<2min).
	if res.ActiveHours != "" {
		t.Errorf("expected no active_hours reminder before a window is set, got %q", res.ActiveHours)
	}

	// Set a window that excludes now.
	var setRes model.SetActiveHoursResult
	sp := model.SetActiveHoursParams{Window: windowExcludingNow()}
	if err := env.client.Call(model.MethodSetActiveHours, sp, &setRes); err != nil {
		t.Fatalf("set-active-hours: %v", err)
	}
	if setRes.ActiveHours == "" {
		t.Error("expected set result to echo the window")
	}
	if setRes.Reclamped < 1 {
		t.Errorf("expected >=1 schedule re-clamped, got %d", setRes.Reclamped)
	}

	// The schedule must now be suppressed and clamped into the future window.
	var show model.ShowResult
	if err := env.client.Call(model.MethodShow, model.ShowParams{ID: res.ID}, &show); err != nil {
		t.Fatalf("show: %v", err)
	}
	if !show.Suppressed {
		t.Error("expected schedule suppressed after setting an excluding window")
	}
	next, err := time.Parse(time.RFC3339, show.NextFireUTC)
	if err != nil {
		t.Fatalf("parse next_fire_utc: %v", err)
	}
	if until := time.Until(next); until < 30*time.Minute {
		t.Errorf("after set, next fire %s is only %s away; expected clamp to window opening", next, until)
	}

	// Health reflects the live window.
	var health model.HealthResult
	if err := env.client.Call(model.MethodHealth, nil, &health); err != nil {
		t.Fatalf("health: %v", err)
	}
	if health.ActiveHours == "" {
		t.Error("health should report the live-set window")
	}

	// Clear it: schedule returns to the raw (soon) slot.
	var clearRes model.SetActiveHoursResult
	if err := env.client.Call(model.MethodSetActiveHours, model.SetActiveHoursParams{Clear: true}, &clearRes); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if !clearRes.Cleared {
		t.Error("expected cleared=true")
	}
	// Fresh struct: Suppressed/ActiveHours are omitempty, so a cleared response
	// omits them — decoding into the prior `show` would leave stale values.
	var afterClear model.ShowResult
	if err := env.client.Call(model.MethodShow, model.ShowParams{ID: res.ID}, &afterClear); err != nil {
		t.Fatalf("show after clear: %v", err)
	}
	if afterClear.Suppressed || afterClear.ActiveHours != "" {
		t.Errorf("after clear, schedule should not be suppressed/windowed; got suppressed=%v active_hours=%q", afterClear.Suppressed, afterClear.ActiveHours)
	}
	next, _ = time.Parse(time.RFC3339, afterClear.NextFireUTC)
	if until := time.Until(next); until > 2*time.Minute {
		t.Errorf("after clear, next fire %s should be the raw slot (<2min), got %s away", next, until)
	}
}

func TestSetActiveHours_InvalidWindowRejected(t *testing.T) {
	env := setupActiveHoursDaemon(t, "", true)
	var res model.SetActiveHoursResult
	err := env.client.Call(model.MethodSetActiveHours, model.SetActiveHoursParams{Window: "8am-10pm"}, &res)
	if err == nil {
		t.Fatal("expected error for malformed window")
	}
}

// TestActiveHours_HealthReportsWindow verifies the daemon-wide window surfaces in health.
func TestActiveHours_HealthReportsWindow(t *testing.T) {
	env := setupActiveHoursDaemon(t, "08:00-22:00", true)

	var health model.HealthResult
	if err := env.client.Call(model.MethodHealth, nil, &health); err != nil {
		t.Fatalf("health: %v", err)
	}
	if health.ActiveHours == "" {
		t.Fatal("expected health to report active_hours")
	}
	if want := "08:00-22:00"; health.ActiveHours[:len(want)] != want {
		t.Errorf("health active_hours = %q, want it to start with %q", health.ActiveHours, want)
	}
}

// TestActiveHours_FiresInsideWindow proves the live run loop actually fires a due
// schedule when the current time is inside the window (end-to-end, real fire).
func TestActiveHours_FiresInsideWindow(t *testing.T) {
	env := setupActiveHoursDaemon(t, windowIncludingNow(), false)

	// Pre-seed a schedule due ~now so the run loop fires it promptly.
	ds := store.NewDesiredStore(env.cfg.DataRepo)
	if err := ds.EnsureDir(); err != nil {
		t.Fatalf("ensure desired dir: %v", err)
	}
	rs := store.NewRuntimeStore(env.cfg.State.Path)
	if err := rs.EnsureDir(); err != nil {
		t.Fatalf("ensure runtime dir: %v", err)
	}

	id := "sch_ah_fire"
	desired := &model.DesiredState{
		ID: id, Name: "fires", CreatedAt: time.Now(),
		Trigger: model.TriggerConfig{Type: model.TriggerTypeCron, Cron: "* * * * *"},
		Command: "true", Status: model.StatusActive,
	}
	if err := ds.Write(desired); err != nil {
		t.Fatalf("write desired: %v", err)
	}
	commitFile(t, env.dataDir, ds.FilePath(id), "test: seed firing schedule")
	// NextFireAt in the (very recent) past => due immediately on first tick.
	if err := rs.Write(&model.RuntimeState{ID: id, NextFireAt: time.Now().Add(-time.Second)}); err != nil {
		t.Fatalf("write runtime: %v", err)
	}

	if err := env.daemon.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForSocket(t, env.client)

	// Poll for a fire entry.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if countType(readRunLog(t, env, id), model.LogTypeFire) > 0 {
			return // fired inside the window — success
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("schedule did not fire inside the active-hours window within 5s; log=%+v", readRunLog(t, env, id))
}

// TestActiveHours_SuppressesViaRunLoop proves the live run loop's fire-time gate
// suppresses a due schedule when now is OUTSIDE the window: no fire runs, a
// suppressed entry is logged, and NextFireAt is deferred to the window opening.
func TestActiveHours_SuppressesViaRunLoop(t *testing.T) {
	env := setupActiveHoursDaemon(t, windowExcludingNow(), false)

	ds := store.NewDesiredStore(env.cfg.DataRepo)
	if err := ds.EnsureDir(); err != nil {
		t.Fatalf("ensure desired dir: %v", err)
	}
	rs := store.NewRuntimeStore(env.cfg.State.Path)
	if err := rs.EnsureDir(); err != nil {
		t.Fatalf("ensure runtime dir: %v", err)
	}

	id := "sch_ah_suppress_live"
	desired := &model.DesiredState{
		ID: id, Name: "suppressed-live", CreatedAt: time.Now(),
		Trigger: model.TriggerConfig{Type: model.TriggerTypeCron, Cron: "* * * * *"},
		Command: "touch " + env.cfg.State.LogsPath + "/SHOULD_NOT_EXIST",
		Status:  model.StatusActive,
	}
	if err := ds.Write(desired); err != nil {
		t.Fatalf("write desired: %v", err)
	}
	commitFile(t, env.dataDir, ds.FilePath(id), "test: seed suppressed schedule")
	// Due ~1s in the PAST: startup reconcile classifies this as a within-grace
	// missed fire and leaves it due, so the run loop's fire-time GATE (not the
	// startup re-clamp, which only touches FUTURE fires) is what suppresses it —
	// the safety-net path for a fire that comes due during closed hours.
	if err := rs.Write(&model.RuntimeState{ID: id, NextFireAt: time.Now().Add(-1 * time.Second)}); err != nil {
		t.Fatalf("write runtime: %v", err)
	}

	if err := env.daemon.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForSocket(t, env.client)

	// Poll for a suppressed entry.
	var entries []*model.RunLogEntry
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		entries = readRunLog(t, env, id)
		if countType(entries, model.LogTypeSuppressed) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if countType(entries, model.LogTypeSuppressed) == 0 {
		t.Fatalf("expected a suppressed entry from the run-loop gate; log=%+v", entries)
	}
	if got := countType(entries, model.LogTypeFire); got != 0 {
		t.Errorf("command must not run while suppressed, got %d fire entries", got)
	}
	// The command would have created this marker file if it ran — it must not exist.
	if _, err := os.Stat(env.cfg.State.LogsPath + "/SHOULD_NOT_EXIST"); err == nil {
		t.Error("suppressed command actually executed (marker file created)")
	}

	// NextFireAt must have been deferred to the window opening (well in the future).
	var show model.ShowResult
	if err := env.client.Call(model.MethodShow, model.ShowParams{ID: id}, &show); err != nil {
		t.Fatalf("show: %v", err)
	}
	next, err := time.Parse(time.RFC3339, show.NextFireUTC)
	if err != nil {
		t.Fatalf("parse next_fire_utc %q: %v", show.NextFireUTC, err)
	}
	if until := time.Until(next); until < 30*time.Minute {
		t.Errorf("after suppression next fire %s is only %s away; expected deferral to the window opening", next, until)
	}
}
