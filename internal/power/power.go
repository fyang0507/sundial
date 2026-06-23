// Package power wraps macOS `pmset` wake scheduling so a sleeping Mac can be
// woken shortly before a due schedule fires. The watchdog (see internal/daemon)
// fires schedules on time once the machine is awake, but `caffeinate -i` only
// blocks idle sleep while the daemon runs — it cannot wake a machine that has
// already gone to sleep. `pmset schedule wakeorpoweron` asks the SMC/RTC to
// wake (or power on) the machine at an absolute timestamp, which is the missing
// piece.
//
// Scheduling/cancelling a wake event requires root, so the daemon shells out via
// `sudo -n` (non-interactive). We never edit sudoers ourselves; SudoersLine
// returns the exact NOPASSWD line for the user to install. If permission is
// absent or pmset is unavailable, the daemon disables wake management gracefully
// (it never breaks scheduling). This package mirrors internal/launchd's
// CommandRunner/DefaultRunner pattern so the daemon can inject a mock in tests
// and never shell out to real pmset/sudo.
package power

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// PmsetPath is the absolute path to the macOS pmset binary. We hardcode the
// absolute path (rather than relying on $PATH) because it is the value that
// appears in the recommended sudoers NOPASSWD line: sudoers matches on the
// fully-qualified command path, so the path we invoke must match the path the
// user authorized exactly.
const PmsetPath = "/usr/bin/pmset"

// CommandRunner abstracts command execution so tests can provide a mock.
// Mirrors internal/launchd.CommandRunner.
type CommandRunner interface {
	Run(name string, args ...string) ([]byte, error)
}

type realRunner struct{}

func (r *realRunner) Run(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// DefaultRunner returns the real CommandRunner that executes via os/exec.
func DefaultRunner() CommandRunner {
	return &realRunner{}
}

// FormatWakeTime formats t in the layout pmset requires: "MM/dd/yy HH:mm:ss"
// (Go reference layout "01/02/06 15:04:05"). pmset interprets the timestamp in
// the machine's local time, so we format in time.Local to match — formatting in
// UTC would schedule the wake at the wrong wall-clock moment.
func FormatWakeTime(t time.Time) string {
	return t.In(time.Local).Format("01/02/06 15:04:05")
}

// Available reports whether pmset exists and works. The probe runs `pmset -g`,
// which only reads current power settings and needs no root, and treats a clean
// exit as available. A non-darwin host (or a stripped image without pmset) will
// error and report unavailable, which lets the daemon disable wake management
// rather than crash.
func Available(runner CommandRunner) bool {
	_, err := runner.Run(PmsetPath, "-g")
	return err == nil
}

// HasPermission reports whether we can run pmset under sudo non-interactively.
// The probe runs `sudo -n /usr/bin/pmset -g sched`: `-n` makes sudo fail
// immediately rather than prompt for a password, so a clean exit means the
// NOPASSWD sudoers rule is in place. Any error (including sudo suppressing a
// password prompt under -n) means we lack permission. `-g sched` only reads the
// current scheduled events, so the probe is side-effect-free — it never
// schedules or cancels a wake.
func HasPermission(runner CommandRunner) bool {
	_, err := runner.Run("sudo", "-n", PmsetPath, "-g", "sched")
	return err == nil
}

// Schedule registers a one-off wake (or power-on) event at t via
//
//	sudo -n /usr/bin/pmset schedule wakeorpoweron "MM/dd/yy HH:mm:ss"
//
// `wakeorpoweron` wakes the machine if asleep, or powers it on if it was shut
// down. Errors are wrapped with the combined command output so callers (and the
// daemon's WARN log) can see why pmset rejected the request.
func Schedule(runner CommandRunner, t time.Time) error {
	stamp := FormatWakeTime(t)
	out, err := runner.Run("sudo", "-n", PmsetPath, "schedule", "wakeorpoweron", stamp)
	if err != nil {
		return fmt.Errorf("pmset schedule wakeorpoweron %q failed: %s: %w", stamp, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// Cancel removes exactly the one-off wake event at t via
//
//	sudo -n /usr/bin/pmset schedule cancel wakeorpoweron "MM/dd/yy HH:mm:ss"
//
// We cancel the single event we own by repeating its exact type and timestamp.
// We deliberately NEVER use `pmset schedule cancelall`, which would delete wake
// events scheduled by other apps (e.g. Time Machine, calendar wake) and is not
// ours to touch.
func Cancel(runner CommandRunner, t time.Time) error {
	stamp := FormatWakeTime(t)
	out, err := runner.Run("sudo", "-n", PmsetPath, "schedule", "cancel", "wakeorpoweron", stamp)
	if err != nil {
		return fmt.Errorf("pmset schedule cancel wakeorpoweron %q failed: %s: %w", stamp, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// SudoersLine returns the exact NOPASSWD sudoers line we recommend the user
// install so the daemon can run pmset under `sudo -n`.
//
// The scope is the pmset binary path with no argument restriction:
//
//	<user> ALL=(root) NOPASSWD: /usr/bin/pmset
//
// This is the tightest scope consistent with the three commands we issue —
// `-g sched` (the HasPermission probe), `schedule wakeorpoweron ...`, and
// `schedule cancel wakeorpoweron ...`. We could in principle pin the exact
// argv, but the wake timestamp varies on every call, so an argument-restricted
// rule cannot match a moving timestamp without a wildcard that is no tighter in
// practice; binary-path scope is the honest minimum.
//
// Security tradeoff: pmset can change a wide range of power-management settings
// (sleep timers, hibernation mode, scheduled power events), so granting
// passwordless pmset is not zero-risk. That is precisely why wake management is
// opt-in (wake.enabled, default false) and why we print this line for the user
// to install deliberately rather than ever editing sudoers ourselves.
func SudoersLine(user string) string {
	return fmt.Sprintf("%s ALL=(root) NOPASSWD: %s", user, PmsetPath)
}
