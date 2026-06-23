package daemon

import (
	"encoding/json"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"github.com/fyang0507/sundial/internal/model"
	"github.com/fyang0507/sundial/internal/power"
)

// wakeStateFile is the reserved filename, under the runtime state directory,
// where we persist the single pmset wake event the daemon currently owns. The
// leading underscore keeps it out of the sch_*.json glob the RuntimeStore uses,
// so it is never mistaken for a schedule's runtime state.
const wakeStateFile = "_wake.json"

// persistedWake is the on-disk shape of our managed wake event. We store the
// absolute instant (not the lead-adjusted fire) so a restarted daemon can cancel
// exactly the pmset event it previously scheduled, regardless of config changes.
type persistedWake struct {
	WakeAt time.Time `json:"wake_at"`
}

// wakeStatePath returns the path to the persisted wake-state file, or "" if no
// state directory is configured (e.g. some unit tests construct a daemon with an
// empty State.Path — they never enable wake, so persistence is a no-op there).
func (d *Daemon) wakeStatePath() string {
	if d.cfg.State.Path == "" {
		return ""
	}
	return filepath.Join(d.cfg.State.Path, wakeStateFile)
}

// loadManagedWake reads the persisted wake event into d.managedWakeAt. It is
// best-effort: a missing file (the common case) or any read/parse error simply
// leaves managedWakeAt zero ("we own no event"). Called once from Start().
func (d *Daemon) loadManagedWake() {
	path := d.wakeStatePath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// ErrNotExist is expected on a fresh install; anything else is logged but
		// not fatal — worst case we orphan one stale pmset event from a prior run.
		if !os.IsNotExist(err) {
			log.Printf("WARN: wake: failed to read persisted wake state %s: %v", path, err)
		}
		return
	}
	var pw persistedWake
	if err := json.Unmarshal(data, &pw); err != nil {
		log.Printf("WARN: wake: failed to parse persisted wake state %s: %v", path, err)
		return
	}
	d.wakeMu.Lock()
	d.managedWakeAt = pw.WakeAt
	d.wakeMu.Unlock()
}

// saveManagedWake persists (or clears) the managed wake event. Caller holds
// wakeMu. Best-effort: a write failure only risks orphaning a stale event after
// a restart, never a failed fire.
func (d *Daemon) saveManagedWake(t time.Time) {
	path := d.wakeStatePath()
	if path == "" {
		return
	}
	if t.IsZero() {
		// No event owned — remove the file so a future restart sees "none".
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			log.Printf("WARN: wake: failed to remove persisted wake state %s: %v", path, err)
		}
		return
	}
	data, err := json.MarshalIndent(persistedWake{WakeAt: t}, "", "  ")
	if err != nil {
		log.Printf("WARN: wake: failed to marshal wake state: %v", err)
		return
	}
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		log.Printf("WARN: wake: failed to write persisted wake state %s: %v", path, err)
	}
}

// updateWakeSchedule reconciles the single pmset wake event the daemon owns with
// the soonest upcoming fire across all active schedules. It is cheap and safe to
// call on every run-loop tick: when nothing changed it is a no-op.
//
// soonest is the earliest NextFireAt across active schedules (zero = none). The
// wake is scheduled lead_time BEFORE soonest so the machine has resumed (and the
// watchdog loop has ticked) by the time the fire is due — pmset wakes the machine
// but the daemon still does the firing.
//
// All pmset interaction is best-effort: any error logs a WARN and is swallowed so
// a misbehaving pmset never breaks reconciliation or firing. The caller must NOT
// hold d.mu (we may shell out here); this method takes only d.wakeMu.
func (d *Daemon) updateWakeSchedule(soonest time.Time) {
	d.wakeMu.Lock()
	defer d.wakeMu.Unlock()

	// Disabled: tear down any event we still own, then stay out of the way.
	if !d.cfg.Daemon.Wake.Enabled {
		d.clearManagedWakeLocked()
		return
	}

	runner := d.powerRunner
	if runner == nil {
		// Defensive: a daemon constructed without New() (some tests) has no
		// runner. Treat as unavailable rather than panicking.
		d.warnWakeDisabledOnce("pmset runner not configured")
		d.clearManagedWakeLocked()
		return
	}

	// Compute the wake instant FIRST, before any pmset/sudo probe. No active
	// schedule (zero) or a wake time already in the past means "no event needed".
	// Computing first lets a steady-state tick (unchanged soonest) short-circuit
	// below without forking the availability/permission subprocesses — the run
	// loop calls this on every signalWake plus every maxTick, so the common case
	// is "nothing changed".
	var wakeAt time.Time
	if !soonest.IsZero() {
		lead := d.wakeLeadTime()
		candidate := soonest.Add(-lead).Round(time.Second)
		if candidate.After(time.Now()) {
			wakeAt = candidate
		}
	}

	if wakeAt.IsZero() {
		// Tear down any event we own. clearManagedWakeLocked probes only when there
		// is actually something to cancel, so this stays cheap when we own nothing.
		d.clearManagedWakeLocked()
		return
	}

	// Idempotency: same second as the event we already own — nothing to do, and no
	// need to probe pmset.
	if !d.managedWakeAt.IsZero() && wakeAt.Equal(d.managedWakeAt.Round(time.Second)) {
		return
	}

	// A schedule/cancel IS about to happen — only now probe availability +
	// permission. If either fails, disable gracefully: pmset wake is a best-effort
	// enhancement, never a hard dependency.
	if !power.Available(runner) {
		d.warnWakeDisabledOnce("pmset is unavailable on this host")
		d.clearManagedWakeLocked()
		return
	}
	if !power.HasPermission(runner) {
		d.warnWakeDisabledOnce("passwordless sudo for pmset is not configured; add this sudoers line: " + power.SudoersLine(currentUserName()))
		d.clearManagedWakeLocked()
		return
	}
	// Permission is present again — reset the warn guard so a future loss is
	// re-reported.
	d.wakeDisabledWarned = false

	// Replace: cancel the old event (if any) before scheduling the new one, so we
	// never accumulate stale pmset events.
	if !d.managedWakeAt.IsZero() {
		if err := power.Cancel(runner, d.managedWakeAt); err != nil {
			log.Printf("WARN: wake: failed to cancel previous wake at %s: %v",
				d.managedWakeAt.Format(time.RFC3339), err)
		}
	}
	if err := power.Schedule(runner, wakeAt); err != nil {
		log.Printf("WARN: wake: failed to schedule wake at %s: %v",
			wakeAt.Format(time.RFC3339), err)
		// We could not schedule — forget the (now-cancelled) old event so health
		// and future ticks reflect reality.
		d.managedWakeAt = time.Time{}
		d.saveManagedWake(time.Time{})
		return
	}

	log.Printf("wake: scheduled pmset wake at %s (lead %s before fire at %s)",
		wakeAt.Format(time.RFC3339), d.wakeLeadTime(), soonest.Format(time.RFC3339))
	d.managedWakeAt = wakeAt
	d.saveManagedWake(wakeAt)
}

// clearManagedWakeLocked cancels and forgets any pmset event we own. Caller holds
// wakeMu. Best-effort cancel; we forget the event regardless so state stays
// consistent. When wake is disabled we still attempt the cancel only if a runner
// is available, because a previous run may have left an event we should clean up.
func (d *Daemon) clearManagedWakeLocked() {
	if d.managedWakeAt.IsZero() {
		return
	}
	if d.powerRunner != nil && power.Available(d.powerRunner) && power.HasPermission(d.powerRunner) {
		if err := power.Cancel(d.powerRunner, d.managedWakeAt); err != nil {
			log.Printf("WARN: wake: failed to cancel managed wake at %s: %v",
				d.managedWakeAt.Format(time.RFC3339), err)
		}
	}
	d.managedWakeAt = time.Time{}
	d.saveManagedWake(time.Time{})
}

// warnWakeDisabledOnce logs a single WARN explaining why wake management is
// disabled despite being enabled, then suppresses repeats until permission is
// regained. Caller holds wakeMu.
func (d *Daemon) warnWakeDisabledOnce(reason string) {
	if d.wakeDisabledWarned {
		return
	}
	log.Printf("WARN: wake: wake.enabled is set but wake management is disabled: %s", reason)
	d.wakeDisabledWarned = true
}

// wakeLeadTime resolves the configured lead time, falling back to the package
// default if it is empty or unparsable (so a typo degrades safely rather than
// disabling wake).
func (d *Daemon) wakeLeadTime() time.Duration {
	if d.cfg.Daemon.Wake.LeadTime != "" {
		if dur, err := time.ParseDuration(d.cfg.Daemon.Wake.LeadTime); err == nil && dur > 0 {
			return dur
		}
		log.Printf("WARN: wake: invalid wake.lead_time %q, using default %s",
			d.cfg.Daemon.Wake.LeadTime, model.DefaultWakeLeadTime)
	}
	dur, _ := time.ParseDuration(model.DefaultWakeLeadTime)
	return dur
}

// currentUserName returns the daemon's username for the sudoers hint, falling
// back to "<user>" so the printed line is still copy-pasteable (with manual
// substitution) if the lookup fails.
func currentUserName() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "<user>"
}
