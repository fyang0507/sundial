package power

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// mockRunner records every call (name + args) and returns canned output/errors
// keyed by the command's argv. The key is the space-joined argv so a test can
// make `pmset -g` succeed while `sudo -n ... -g sched` fails (or vice versa),
// which Available/HasPermission need to probe independently. If no canned
// response matches, the zero value (nil output, nil error) is returned, i.e. a
// clean exit. Mirrors the recording style of internal/launchd's mockRunner.
type mockRunner struct {
	calls    [][]string
	outputs  map[string][]byte
	errs     map[string]error
	fallback error
}

func newMockRunner() *mockRunner {
	return &mockRunner{outputs: map[string][]byte{}, errs: map[string]error{}}
}

func (m *mockRunner) Run(name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	m.calls = append(m.calls, call)
	key := strings.Join(call, " ")
	if err, ok := m.errs[key]; ok {
		return m.outputs[key], err
	}
	if out, ok := m.outputs[key]; ok {
		return out, nil
	}
	return nil, m.fallback
}

// lastCall returns the most recent recorded argv, or nil if none.
func (m *mockRunner) lastCall() []string {
	if len(m.calls) == 0 {
		return nil
	}
	return m.calls[len(m.calls)-1]
}

func equalArgv(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFormatWakeTime(t *testing.T) {
	// Construct a fixed local time so the formatted string is deterministic
	// regardless of the host's zone: FormatWakeTime renders in time.Local, and
	// a time built with time.Local already carries that zone.
	fixed := time.Date(2026, time.March, 9, 7, 5, 3, 0, time.Local)
	got := FormatWakeTime(fixed)
	want := "03/09/26 07:05:03"
	if got != want {
		t.Fatalf("FormatWakeTime = %q, want %q", got, want)
	}

	// Re-parse to confirm the layout round-trips to the same wall-clock time,
	// guarding the layout itself rather than a hardcoded string.
	parsed, err := time.ParseInLocation("01/02/06 15:04:05", got, time.Local)
	if err != nil {
		t.Fatalf("re-parsing %q: %v", got, err)
	}
	if !parsed.Equal(fixed) {
		t.Errorf("round-trip = %v, want %v", parsed, fixed)
	}
}

func TestSchedule(t *testing.T) {
	runner := newMockRunner()
	when := time.Date(2026, time.March, 9, 7, 5, 3, 0, time.Local)

	if err := Schedule(runner, when); err != nil {
		t.Fatalf("Schedule returned error: %v", err)
	}

	want := []string{"sudo", "-n", PmsetPath, "schedule", "wakeorpoweron", "03/09/26 07:05:03"}
	if got := runner.lastCall(); !equalArgv(got, want) {
		t.Errorf("Schedule argv = %v, want %v", got, want)
	}
}

func TestScheduleError(t *testing.T) {
	runner := newMockRunner()
	when := time.Date(2026, time.March, 9, 7, 5, 3, 0, time.Local)
	key := strings.Join([]string{"sudo", "-n", PmsetPath, "schedule", "wakeorpoweron", "03/09/26 07:05:03"}, " ")
	runner.errs[key] = errors.New("boom")
	runner.outputs[key] = []byte("sudo: a password is required")

	err := Schedule(runner, when)
	if err == nil {
		t.Fatal("expected error from Schedule")
	}
	// The combined output must be surfaced for the daemon's WARN log.
	if !strings.Contains(err.Error(), "password is required") {
		t.Errorf("error %q should include combined output", err.Error())
	}
}

func TestCancel(t *testing.T) {
	runner := newMockRunner()
	when := time.Date(2026, time.December, 31, 23, 59, 0, 0, time.Local)

	if err := Cancel(runner, when); err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}

	want := []string{"sudo", "-n", PmsetPath, "schedule", "cancel", "wakeorpoweron", "12/31/26 23:59:00"}
	if got := runner.lastCall(); !equalArgv(got, want) {
		t.Errorf("Cancel argv = %v, want %v", got, want)
	}

	// Guard against ever issuing cancelall, which would nuke other apps' events.
	for _, c := range runner.calls {
		for _, a := range c {
			if a == "cancelall" {
				t.Fatal("Cancel must never issue cancelall")
			}
		}
	}
}

func TestAvailable(t *testing.T) {
	t.Run("available", func(t *testing.T) {
		runner := newMockRunner() // clean exit for pmset -g
		if !Available(runner) {
			t.Error("expected Available to be true on clean exit")
		}
		want := []string{PmsetPath, "-g"}
		if got := runner.lastCall(); !equalArgv(got, want) {
			t.Errorf("Available argv = %v, want %v", got, want)
		}
	})

	t.Run("unavailable", func(t *testing.T) {
		runner := newMockRunner()
		runner.errs[strings.Join([]string{PmsetPath, "-g"}, " ")] = errors.New("not found")
		if Available(runner) {
			t.Error("expected Available to be false when pmset errors")
		}
	})
}

func TestHasPermission(t *testing.T) {
	probeKey := strings.Join([]string{"sudo", "-n", PmsetPath, "-g", "sched"}, " ")

	t.Run("granted", func(t *testing.T) {
		runner := newMockRunner() // clean exit
		if !HasPermission(runner) {
			t.Error("expected HasPermission to be true on clean exit")
		}
		want := []string{"sudo", "-n", PmsetPath, "-g", "sched"}
		if got := runner.lastCall(); !equalArgv(got, want) {
			t.Errorf("HasPermission argv = %v, want %v", got, want)
		}
	})

	t.Run("denied", func(t *testing.T) {
		runner := newMockRunner()
		runner.errs[probeKey] = errors.New("sudo: a password is required")
		if HasPermission(runner) {
			t.Error("expected HasPermission to be false when sudo -n fails")
		}
	})
}

func TestSudoersLine(t *testing.T) {
	line := SudoersLine("alice")
	if !strings.Contains(line, "alice") {
		t.Errorf("SudoersLine %q should contain the user", line)
	}
	if !strings.Contains(line, PmsetPath) {
		t.Errorf("SudoersLine %q should contain the pmset path", line)
	}
	if !strings.Contains(line, "NOPASSWD") {
		t.Errorf("SudoersLine %q should be a NOPASSWD rule", line)
	}
}
