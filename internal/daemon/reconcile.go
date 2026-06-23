package daemon

import (
	"log"
	"time"

	"github.com/fyang0507/sundial/internal/model"
	"github.com/fyang0507/sundial/internal/trigger"
)

// maxMissEntries is the cap on individual miss log entries per schedule during
// a single startup reconciliation. Beyond this, a single miss_summary is written.
const maxMissEntries = 10

// reconcile synchronizes the in-memory active schedules with the desired and
// runtime stores. When isStartup is true, it also handles missed fires.
func (d *Daemon) reconcile(isStartup bool) error {
	// Read all desired state.
	desiredList, err := d.desiredStore.List()
	if err != nil {
		return err
	}

	// Read all runtime state.
	runtimeList, err := d.runtimeStore.List()
	if err != nil {
		return err
	}

	// Build maps by ID.
	desiredMap := make(map[string]*model.DesiredState, len(desiredList))
	for _, ds := range desiredList {
		desiredMap[ds.ID] = ds
	}
	runtimeMap := make(map[string]*model.RuntimeState, len(runtimeList))
	for _, rs := range runtimeList {
		runtimeMap[rs.ID] = rs
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// Track which IDs are still active after reconciliation.
	activeIDs := make(map[string]struct{})

	// Process desired states.
	for id, ds := range desiredMap {
		rs := runtimeMap[id]

		switch ds.Status {
		case model.StatusActive, model.StatusPaused:
			// Parse trigger.
			trig, err := trigger.ParseTrigger(ds.Trigger)
			if err != nil {
				log.Printf("WARN: schedule %s (%s): failed to parse trigger: %v", id, ds.Name, err)
				continue
			}

			if rs == nil {
				// No runtime -> create one.
				nextFire := time.Time{} // zero for paused
				if ds.Status == model.StatusActive {
					nextFire = trig.NextFireTime(time.Now())
				}
				rs = &model.RuntimeState{
					ID:         id,
					NextFireAt: nextFire,
				}
				if err := d.runtimeStore.Write(rs); err != nil {
					log.Printf("WARN: schedule %s: failed to write runtime state: %v", id, err)
					continue
				}
			} else if ds.Status == model.StatusPaused {
				// Ensure paused schedules have zero NextFireAt.
				rs.NextFireAt = time.Time{}
			}

			d.schedules[id] = &activeSchedule{
				desired: ds,
				runtime: rs,
				trigger: trig,
			}
			activeIDs[id] = struct{}{}

		case model.StatusCompleted, model.StatusRemoved:
			// Completed or removed desired + runtime exists -> delete runtime.
			if rs != nil {
				if err := d.runtimeStore.Delete(id); err != nil {
					log.Printf("WARN: schedule %s: failed to delete runtime state: %v", id, err)
				}
			}
			delete(d.schedules, id)
		}
	}

	// Handle orphans: runtime exists but no desired state.
	for id := range runtimeMap {
		if _, ok := desiredMap[id]; !ok {
			log.Printf("WARN: orphaned runtime state for %s (no desired state found)", id)
			delete(d.schedules, id)
		}
	}

	// Remove schedules that are no longer active.
	for id := range d.schedules {
		if _, ok := activeIDs[id]; !ok {
			delete(d.schedules, id)
		}
	}

	if isStartup {
		// Classify any fires missed while the daemon was offline. Within-grace
		// misses are LEFT with NextFireAt in the past so the run loop's first
		// fireDueSchedules pass executes them once; this classifier never
		// executes commands itself (the daemon stays the single writer, and we
		// avoid running commands while holding d.mu).
		d.handleMissedFires()
	} else {
		// Non-startup reconcile (e.g. RPC-driven reload): we are not recovering
		// from downtime, so any past NextFireAt should be advanced rather than
		// treated as a missed fire to coalesce.
		d.advanceAllSchedules()
	}

	d.signalWake()

	return nil
}

// reconcileMissedFires is the wake-path entry point for missed-fire handling.
// It exists separately from reconcile() so the run loop can re-run the same
// classification after a detected wall-clock gap (system sleep) WITHOUT
// re-reading the stores or rebuilding the schedule map. It takes d.mu the same
// way startup reconcile does and never executes commands while holding it —
// the within-grace misses it leaves in the past are fired by the run loop's
// subsequent fireDueSchedules pass.
func (d *Daemon) reconcileMissedFires() {
	d.mu.Lock()
	d.handleMissedFires()
	d.mu.Unlock()
	d.signalWake()
}

// handleMissedFires classifies each active schedule whose NextFireAt is in the
// past and adjusts its runtime state. It does NOT execute commands — that is the
// run loop's job via fireDueSchedules. The classification rules are:
//
//   - cron/solar within the grace window: LEAVE NextFireAt in the past so the
//     next fireDueSchedules pass fires it exactly once (coalescing many missed
//     occurrences into a single catch-up fire).
//   - cron/solar beyond grace: log the missed fires (capped individual entries
//     plus a miss_summary) and advance NextFireAt to the next FUTURE fire time
//     so the catch-up fire is suppressed.
//   - at within grace: leave in the past to fire once.
//   - at beyond grace: log one miss and complete with reason "missed".
//   - poll: if timed out, complete with reason "timeout"; otherwise advance past
//     the missed checks (a missed poll check carries no meaning — the condition
//     may or may not still hold).
//
// Callers must hold d.mu (reconcile holds it via its defer; reconcileMissedFires
// takes it explicitly).
func (d *Daemon) handleMissedFires() {
	now := time.Now()

	for id, sched := range d.schedules {
		if sched.desired.Status == model.StatusPaused {
			continue
		}

		nextFire := sched.runtime.NextFireAt
		if nextFire.IsZero() || !nextFire.Before(now) {
			continue
		}

		// A pending precondition backoff retry stores a FUTURE NextFireAt that may
		// now be in the past (we slept through the retry, or the daemon restarted
		// mid-backoff). This is NOT a missed fire: leave NextFireAt in the past so
		// the next fireDueSchedules pass re-enters execute -> checkPrecondition,
		// which re-evaluates the gate and lets deferPrecondition re-apply the
		// correct backoff/give-up using the preserved first-deferred-at anchor.
		// Classifying it as a miss here would emit spurious "daemon was not running"
		// entries, silently discard the in-flight backoff (leaving a stale attempt
		// count and anchor), or prematurely complete an `at` still inside its budget.
		if sched.runtime.PreconditionFirstDeferredAt != nil {
			log.Printf("schedule %s (%s): pending precondition retry past due, will re-check on next tick",
				id, sched.desired.Name)
			continue
		}

		// Poll triggers: missed checks are meaningless — the condition may or
		// may not still hold. If the timeout expired while offline, complete
		// without firing. Otherwise advance to the next interval.
		if sched.desired.Trigger.Type == model.TriggerTypePoll {
			if d.isPollTimedOut(sched) {
				log.Printf("schedule %s (%s): poll timeout expired while daemon was stopped, completing",
					id, sched.desired.Name)
				d.completeSchedule(sched, model.CompletionTimeout)
				continue
			}
			sched.runtime.NextFireAt = sched.trigger.NextFireTime(now)
			if err := d.runtimeStore.Write(sched.runtime); err != nil {
				log.Printf("WARN: schedule %s: failed to persist runtime after poll advance: %v", id, err)
			}
			log.Printf("schedule %s (%s): poll trigger advanced past missed checks",
				id, sched.desired.Name)
			continue
		}

		elapsed := now.Sub(nextFire)

		if elapsed <= d.missGracePeriod() {
			// Within grace: leave NextFireAt in the past. The run loop's
			// fireDueSchedules pass (which runs right after startup reconcile,
			// and again after a wake-gap detection) will fire it once. We do
			// not execute here — that keeps command execution off the d.mu
			// critical section and out of the single-writer classifier.
			log.Printf("schedule %s (%s): missed fire within grace period (%.0fs ago), will fire on next tick",
				id, sched.desired.Name, elapsed.Seconds())
		} else if sched.desired.Trigger.Type == model.TriggerTypeAt {
			// `at` fires exactly once. Beyond grace, log one miss and complete
			// with reason "missed" so the schedule doesn't sit active-but-inert.
			log.Printf("schedule %s (%s): at trigger missed (%.0fs past fire time), completing",
				id, sched.desired.Name, elapsed.Seconds())
			entry := &model.RunLogEntry{
				Timestamp:    time.Now(),
				Type:         model.LogTypeMiss,
				ScheduleID:   id,
				Reason:       "daemon was not running",
				ScheduledFor: &nextFire,
			}
			if err := d.runLogStore.Append(entry); err != nil {
				log.Printf("WARN: schedule %s: failed to write miss entry: %v", id, err)
			}
			d.completeSchedule(sched, model.CompletionMissed)
		} else {
			// cron/solar beyond grace: log the misses and advance NextFireAt to
			// the next future fire so the catch-up fire is suppressed.
			d.logMissedFires(sched, nextFire, now)
			next := sched.trigger.NextFireTime(now)
			sched.runtime.NextFireAt = next
			if err := d.runtimeStore.Write(sched.runtime); err != nil {
				log.Printf("WARN: schedule %s: failed to persist runtime after miss advance: %v", id, err)
			}
		}
	}
}

// missGracePeriod resolves the configured missed-fire grace window, falling back
// to the package default if it is empty or unparsable (so a typo degrades safely
// to the documented 60s rather than disabling miss handling). It mirrors
// wakeLeadTime's degrade-gracefully shape: a malformed value logs a single WARN
// per call and uses the default. The window is the threshold handleMissedFires
// uses to decide whether a fire missed while the daemon was offline/asleep is
// still executed once (within grace) or logged as a miss and advanced (beyond).
func (d *Daemon) missGracePeriod() time.Duration {
	if d.cfg.Daemon.MissGracePeriod != "" {
		if dur, err := time.ParseDuration(d.cfg.Daemon.MissGracePeriod); err == nil && dur >= 0 {
			return dur
		}
		log.Printf("WARN: invalid miss_grace_period %q, using default %s",
			d.cfg.Daemon.MissGracePeriod, model.DefaultMissGracePeriod)
	}
	dur, _ := time.ParseDuration(model.DefaultMissGracePeriod)
	return dur
}

// logMissedFires records missed fire entries for a schedule. It computes all
// fire times between the missed NextFireAt and now, capping individual entries
// at maxMissEntries and writing a summary for the rest.
func (d *Daemon) logMissedFires(sched *activeSchedule, from, to time.Time) {
	var missedTimes []time.Time

	// Walk forward from the missed fire time to find all missed fires.
	t := from
	for {
		if t.After(to) {
			break
		}
		missedTimes = append(missedTimes, t)
		next := sched.trigger.NextFireTime(t)
		if next.IsZero() || next.After(to) {
			break
		}
		t = next
		// Safety cap to prevent infinite loops.
		if len(missedTimes) > 10000 {
			break
		}
	}

	totalMissed := len(missedTimes)
	if totalMissed == 0 {
		return
	}

	log.Printf("schedule %s (%s): %d missed fires while daemon was stopped",
		sched.desired.ID, sched.desired.Name, totalMissed)

	// Write individual miss entries up to the cap.
	written := 0
	for i, mt := range missedTimes {
		if i >= maxMissEntries {
			break
		}
		entry := &model.RunLogEntry{
			Timestamp:    time.Now(),
			Type:         model.LogTypeMiss,
			ScheduleID:   sched.desired.ID,
			Reason:       "daemon was not running",
			ScheduledFor: &mt,
		}
		if err := d.runLogStore.Append(entry); err != nil {
			log.Printf("WARN: schedule %s: failed to write miss entry: %v", sched.desired.ID, err)
		}
		written++
	}

	// If there are more misses than the cap, write a summary.
	remaining := totalMissed - written
	if remaining > 0 {
		firstMissed := missedTimes[0]
		lastMissed := missedTimes[len(missedTimes)-1]
		entry := &model.RunLogEntry{
			Timestamp:  time.Now(),
			Type:       model.LogTypeMissSummary,
			ScheduleID: sched.desired.ID,
			Reason:     "daemon was not running",
			Count:      totalMissed,
			From:       firstMissed.UTC().Format(time.RFC3339),
			To:         lastMissed.UTC().Format(time.RFC3339),
		}
		if err := d.runLogStore.Append(entry); err != nil {
			log.Printf("WARN: schedule %s: failed to write miss summary: %v", sched.desired.ID, err)
		}
	}
}

// advanceAllSchedules recomputes NextFireAt for each active schedule using
// the trigger's NextFireTime and persists the updated runtime state.
// Paused schedules are skipped — their NextFireAt stays at zero.
func (d *Daemon) advanceAllSchedules() {
	now := time.Now()
	for id, sched := range d.schedules {
		if sched.desired.Status == model.StatusPaused {
			continue
		}
		next := sched.trigger.NextFireTime(now)
		sched.runtime.NextFireAt = next
		if err := d.runtimeStore.Write(sched.runtime); err != nil {
			log.Printf("WARN: schedule %s: failed to persist runtime state: %v", id, err)
		}
	}
}
