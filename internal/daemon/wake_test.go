package daemon

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/fyang0507/sundial/internal/model"
	"github.com/fyang0507/sundial/internal/power"
	"github.com/fyang0507/sundial/internal/trigger"
)

// mockPowerRunner is an injectable power.CommandRunner that records every pmset
// invocation and lets a test toggle availability/permission, so daemon wake
// logic can be exercised without shelling out to real pmset/sudo.
type mockPowerRunner struct {
	mu          sync.Mutex
	available   bool // `pmset -g` succeeds
	permission  bool // `sudo -n pmset -g sched` succeeds
	scheduleErr error // returned from the Schedule invocation when set

	scheduleCalls []string // timestamps passed to `schedule wakeorpoweron`
	cancelCalls   []string // timestamps passed to `schedule cancel wakeorpoweron`
}

func (m *mockPowerRunner) Run(name string, args ...string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Classify the invocation the same way the power package builds it.
	switch {
	case name == power.PmsetPath && len(args) == 1 && args[0] == "-g":
		// Available probe.
		if !m.available {
			return nil, fmt.Errorf("pmset unavailable")
		}
		return nil, nil
	case name == "sudo" && len(args) == 4 && args[1] == power.PmsetPath && args[2] == "-g" && args[3] == "sched":
		// HasPermission probe.
		if !m.permission {
			return nil, fmt.Errorf("no permission")
		}
		return nil, nil
	case name == "sudo" && len(args) == 5 && args[2] == "schedule" && args[3] == "wakeorpoweron":
		m.scheduleCalls = append(m.scheduleCalls, args[4])
		return nil, m.scheduleErr
	case name == "sudo" && len(args) == 6 && args[2] == "schedule" && args[3] == "cancel" && args[4] == "wakeorpoweron":
		m.cancelCalls = append(m.cancelCalls, args[5])
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected command: %s %v", name, args)
	}
}

func (m *mockPowerRunner) schedules() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.scheduleCalls...)
}

func (m *mockPowerRunner) cancels() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.cancelCalls...)
}

// newWakeTestDaemon builds a test daemon with wake enabled and the given mock
// runner injected. State.Path points at a temp dir so persistence works.
func newWakeTestDaemon(t *testing.T, runner power.CommandRunner) *Daemon {
	t.Helper()
	d := newTestDaemon(t)
	d.cfg.State.Path = t.TempDir()
	d.cfg.Daemon.Wake = model.WakeConfig{Enabled: true, LeadTime: "3m"}
	d.powerRunner = runner
	return d
}

func TestUpdateWakeSchedule_DisabledCancelsExisting(t *testing.T) {
	mock := &mockPowerRunner{available: true, permission: true}
	d := newWakeTestDaemon(t, mock)
	d.cfg.Daemon.Wake.Enabled = false

	// Pretend a previous run scheduled an event we now own.
	prev := time.Now().Add(time.Hour).Round(time.Second)
	d.managedWakeAt = prev

	d.updateWakeSchedule(time.Now().Add(2 * time.Hour))

	if got := mock.schedules(); len(got) != 0 {
		t.Errorf("expected no schedule calls when disabled, got %v", got)
	}
	if got := mock.cancels(); len(got) != 1 {
		t.Errorf("expected the existing event to be cancelled, got %v", got)
	}
	if !d.managedWakeAt.IsZero() {
		t.Errorf("expected managedWakeAt cleared when disabled, got %v", d.managedWakeAt)
	}
}

func TestUpdateWakeSchedule_SchedulesAtLeadTime(t *testing.T) {
	mock := &mockPowerRunner{available: true, permission: true}
	d := newWakeTestDaemon(t, mock)

	soonest := time.Now().Add(time.Hour)
	d.updateWakeSchedule(soonest)

	calls := mock.schedules()
	if len(calls) != 1 {
		t.Fatalf("expected exactly one schedule call, got %v", calls)
	}
	wantStamp := power.FormatWakeTime(soonest.Add(-3 * time.Minute).Round(time.Second))
	if calls[0] != wantStamp {
		t.Errorf("expected wake at lead-adjusted time %q, got %q", wantStamp, calls[0])
	}
	if d.managedWakeAt.IsZero() {
		t.Error("expected managedWakeAt to be recorded")
	}
}

func TestUpdateWakeSchedule_IdempotentSameSoonest(t *testing.T) {
	mock := &mockPowerRunner{available: true, permission: true}
	d := newWakeTestDaemon(t, mock)

	soonest := time.Now().Add(time.Hour)
	d.updateWakeSchedule(soonest)
	d.updateWakeSchedule(soonest) // same soonest → no-op

	if got := mock.schedules(); len(got) != 1 {
		t.Errorf("expected a single schedule call for repeated identical soonest, got %v", got)
	}
	if got := mock.cancels(); len(got) != 0 {
		t.Errorf("expected no cancel on idempotent call, got %v", got)
	}
}

func TestUpdateWakeSchedule_ChangedSoonestReschedules(t *testing.T) {
	mock := &mockPowerRunner{available: true, permission: true}
	d := newWakeTestDaemon(t, mock)

	first := time.Now().Add(time.Hour)
	d.updateWakeSchedule(first)

	second := time.Now().Add(30 * time.Minute)
	d.updateWakeSchedule(second)

	if got := mock.schedules(); len(got) != 2 {
		t.Errorf("expected two schedule calls (initial + reschedule), got %v", got)
	}
	if got := mock.cancels(); len(got) != 1 {
		t.Errorf("expected one cancel of the first event before rescheduling, got %v", got)
	}
	wantStamp := power.FormatWakeTime(second.Add(-3 * time.Minute).Round(time.Second))
	if calls := mock.schedules(); calls[len(calls)-1] != wantStamp {
		t.Errorf("expected latest wake at %q, got %q", wantStamp, calls[len(calls)-1])
	}
}

func TestUpdateWakeSchedule_NoPermissionDoesNotSchedule(t *testing.T) {
	mock := &mockPowerRunner{available: true, permission: false}
	d := newWakeTestDaemon(t, mock)

	d.updateWakeSchedule(time.Now().Add(time.Hour))

	if got := mock.schedules(); len(got) != 0 {
		t.Errorf("expected no schedule calls without permission, got %v", got)
	}

	// Health should report the missing permission and surface the sudoers hint.
	res, rpcErr := d.handleHealth()
	if rpcErr != nil {
		t.Fatalf("unexpected health error: %v", rpcErr.Message)
	}
	if res.WakePermission {
		t.Error("expected WakePermission=false")
	}
	if !res.WakeEnabled {
		t.Error("expected WakeEnabled=true")
	}
	if res.WakeSudoersHint == "" {
		t.Error("expected a sudoers hint when enabled but permission missing")
	}
}

func TestUpdateWakeSchedule_ZeroSoonestNoEvent(t *testing.T) {
	mock := &mockPowerRunner{available: true, permission: true}
	d := newWakeTestDaemon(t, mock)

	d.updateWakeSchedule(time.Time{})

	if got := mock.schedules(); len(got) != 0 {
		t.Errorf("expected no schedule call for zero soonest, got %v", got)
	}
}

func TestUpdateWakeSchedule_PastSoonestNoEvent(t *testing.T) {
	mock := &mockPowerRunner{available: true, permission: true}
	d := newWakeTestDaemon(t, mock)

	// A fire already in the past → lead-adjusted wake is also in the past.
	d.updateWakeSchedule(time.Now().Add(-time.Hour))

	if got := mock.schedules(); len(got) != 0 {
		t.Errorf("expected no schedule call for past soonest, got %v", got)
	}
}

func TestUpdateWakeSchedule_DisabledIsNoOpWithoutExisting(t *testing.T) {
	mock := &mockPowerRunner{available: true, permission: true}
	d := newWakeTestDaemon(t, mock)
	d.cfg.Daemon.Wake.Enabled = false

	d.updateWakeSchedule(time.Now().Add(time.Hour))

	if got := mock.schedules(); len(got) != 0 {
		t.Errorf("expected no schedule calls when disabled, got %v", got)
	}
	if got := mock.cancels(); len(got) != 0 {
		t.Errorf("expected no cancel calls when disabled with no owned event, got %v", got)
	}
}

// TestUpdateWakeSchedule_ViaActiveSchedules confirms the integration with
// soonestFire: wiring an active schedule and calling updateWakeSchedule with its
// fire time schedules a wake, exercising the same helpers (addSchedule,
// makeCronDesired) the rest of the suite uses.
func TestUpdateWakeSchedule_ViaActiveSchedules(t *testing.T) {
	mock := &mockPowerRunner{available: true, permission: true}
	d := newWakeTestDaemon(t, mock)

	desired := makeCronDesired("sch_wake01", "wake-test", "0 9 * * *")
	trig, err := trigger.ParseTrigger(desired.Trigger)
	if err != nil {
		t.Fatal(err)
	}
	next := trig.NextFireTime(time.Now())
	d.mu.Lock()
	d.schedules["sch_wake01"] = &activeSchedule{
		desired: desired,
		runtime: &model.RuntimeState{ID: "sch_wake01", NextFireAt: next},
		trigger: trig,
	}
	d.mu.Unlock()

	id, soonest := d.soonestFire()
	if id != "sch_wake01" {
		t.Fatalf("expected soonest to be sch_wake01, got %q", id)
	}
	d.updateWakeSchedule(soonest)

	if got := mock.schedules(); len(got) != 1 {
		t.Errorf("expected one schedule call from the active schedule's fire, got %v", got)
	}
}
