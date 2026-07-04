package daemon

import (
	"fmt"
	"log"
	"time"

	"github.com/fyang0507/sundial/internal/model"
)

// maxTick bounds how long the run loop will sleep on a single timer, even when
// the next fire is hours away (or there are no active schedules at all).
//
// Go timers run on the MONOTONIC clock, which macOS SUSPENDS while the system
// sleeps. A single time.NewTimer(time.Until(next)) armed before sleep therefore
// fires late by the entire sleep duration — the daemon would sleep through a
// fire and never notice until the stale timer eventually fired. Capping every
// sleep at maxTick guarantees the loop wakes (in WALL-clock terms) at least once
// a minute after the machine resumes, giving the gap detector (see below) a
// chance to run the missed-fire reconciliation that the frozen timer can't.
const maxTick = 60 * time.Second

// wakeGapThreshold is how far wall-clock time must jump between two loop ticks
// for us to conclude the system slept (rather than merely ticking late under
// load). maxTick + 30s of slack: a normal tick advances the wall clock by ~maxTick,
// so anything materially beyond that is a suspend/resume gap.
const wakeGapThreshold = maxTick + 30*time.Second

// runLoop is the main scheduler loop. It wakes on the soonest fire time (bounded
// by maxTick), fires due schedules, and recalculates when the schedule set
// changes. Because Go timers freeze during system sleep, the loop also tracks
// wall-clock time across iterations and, when it detects a gap larger than one
// bounded tick, re-runs missed-fire classification before firing.
func (d *Daemon) runLoop() {
	lastTick := time.Now()

	// maybeReconcileGap detects a wall-clock gap that exceeds a single bounded
	// tick: the monotonic timer froze through a system sleep. When that happens
	// it re-classifies missed fires (advancing beyond-grace cron/solar, completing
	// timed-out polls and missed `at`s, leaving within-grace ones in the past)
	// so the subsequent fireDueSchedules pass doesn't replay every occurrence that
	// elapsed during sleep. It runs on EITHER select arm — a buffered wake signal
	// queued just before sleep must not mask the gap by winning the race and
	// skipping reconciliation.
	maybeReconcileGap := func(now time.Time) {
		if now.Sub(lastTick) > wakeGapThreshold {
			log.Printf("detected wall-clock gap of %s (likely system sleep), reconciling missed fires",
				now.Sub(lastTick).Round(time.Second))
			d.reconcileMissedFires()
		}
		lastTick = now
	}

	for {
		nextID, nextTime := d.soonestFire()

		// Reconcile the pmset wake event with the soonest fire. This single call
		// covers every mutation path — add/remove/pause/unpause/fire/advance/defer/
		// reconcile all end in signalWake, which re-runs this loop and re-syncs the
		// wake event. It is a no-op when wake is disabled or nothing changed, and
		// never holds d.mu while shelling out to pmset. Use zero time when there is
		// no active schedule so updateWakeSchedule tears down any owned event.
		if nextID == "" {
			d.updateWakeSchedule(time.Time{})
		} else {
			d.updateWakeSchedule(nextTime)
		}

		// Bound the sleep at maxTick so we keep waking for gap/wake detection
		// even when the next fire is far off or there are no active schedules.
		dur := maxTick
		if nextID != "" {
			if until := time.Until(nextTime); until < dur {
				dur = until
			}
		}
		if dur < 0 {
			dur = 0
		}
		timer := time.NewTimer(dur)

		select {
		case <-timer.C:
			maybeReconcileGap(time.Now())
			// Re-detect the machine timezone before firing so follow-local
			// active-hours windows track a mid-run zone change (e.g. laptop
			// traveled NYC -> SFO). Cheap when unchanged; recomputes windows and
			// re-clamps NextFireAt when it changed.
			d.maybeReconcileTimezone()
			// Always attempt to fire: a normally-late tick still has due
			// schedules, and the within-grace misses left in the past by the
			// gap reconciler need firing here.
			d.fireDueSchedules()

		case <-d.wake:
			timer.Stop()
			maybeReconcileGap(time.Now())
			continue

		case <-d.quit:
			timer.Stop()
			return
		}
	}
}

// soonestFire returns the ID and fire time of the schedule with the earliest
// NextFireAt. Returns ("", zero time) if there are no active schedules.
func (d *Daemon) soonestFire() (string, time.Time) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var soonestID string
	var soonestTime time.Time

	for id, sched := range d.schedules {
		nf := sched.runtime.NextFireAt
		if nf.IsZero() {
			continue
		}
		if soonestID == "" || nf.Before(soonestTime) {
			soonestID = id
			soonestTime = nf
		}
	}

	return soonestID, soonestTime
}

// fireDueSchedules finds all schedules whose NextFireAt is at or before now
// and launches their execution concurrently.
func (d *Daemon) fireDueSchedules() {
	now := time.Now()

	d.mu.RLock()
	var due []*activeSchedule
	for _, sched := range d.schedules {
		if !sched.runtime.NextFireAt.IsZero() && !sched.runtime.NextFireAt.After(now) {
			due = append(due, sched)
		}
	}
	d.mu.RUnlock()

	for _, sched := range due {
		sched := sched // capture loop variable
		// Hold sched.mu across the whole fire cycle (execute + advance) so that
		// repeated runLoop ticks while the cycle is in flight skip via TryLock
		// instead of queuing behind it. Without this, a --once + --detach +
		// poll schedule hot-loops: execute releases the mutex on its fast
		// detached-spawn return, the next tick (NextFireAt is still in the
		// past until advanceSchedule deletes the schedule from d.schedules)
		// fires another goroutine, which then queues behind advanceSchedule,
		// and the queue drains by running completeSchedule N times.
		if !sched.mu.TryLock() {
			log.Printf("schedule %s (%s): skipping, previous fire still in progress",
				sched.desired.ID, sched.desired.Name)
			continue
		}
		// Zero NextFireAt so soonestFire skips this schedule until the fire
		// goroutine's advanceSchedule recomputes it (or deletes the schedule
		// entirely for --once completion). Without this, the next runLoop
		// tick re-sees a past NextFireAt, tries to fire again, fails TryLock
		// (we still hold sched.mu), logs "skipping", and tight-loops the CPU
		// until the fire goroutine finishes.
		d.mu.Lock()
		// Capture the fire time we are servicing before zeroing it. A precondition
		// deferral needs it both as the miss's scheduled_for (if we give up) and,
		// for a retry on a recurring trigger, as the anchor for the next regular
		// fire that bounds the backoff.
		intendedFire := sched.runtime.NextFireAt
		sched.runtime.NextFireAt = time.Time{}
		d.mu.Unlock()
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			defer sched.mu.Unlock()
			outcome := d.execute(sched)
			switch {
			case outcome.Suppressed:
				// The fire landed outside active hours: skip the command and defer
				// to the next window opening. Not a precondition deferral (no
				// backoff), not a normal advance (the intended slot never ran).
				d.suppressFire(sched, intendedFire)
			case outcome.Deferred:
				// A precondition held the fire back: arrange a backoff retry (or
				// give up) instead of advancing the regular schedule.
				d.deferPrecondition(sched, intendedFire)
			default:
				d.advanceSchedule(sched)
			}
			d.signalWake()
		}()
	}

}

// advanceSchedule recomputes the next fire time for a single schedule
// and persists the updated runtime state. For --once schedules that have
// already fired, it marks the schedule as completed. For poll schedules
// whose timeout has expired, it marks them as completed with reason "timeout".
//
// Caller (fireDueSchedules) holds sched.mu across execute + advanceSchedule,
// so desiredStore.Write / runtimeStore.Write here cannot race with a
// concurrent RPC mutator (refreshActiveSchedule) of the same schedule.
func (d *Daemon) advanceSchedule(sched *activeSchedule) {
	if sched.desired.Once && sched.runtime.FireCount > 0 {
		d.completeSchedule(sched, model.CompletionTriggered)
		return
	}

	// Poll timeout: complete the schedule after the deadline passes.
	if sched.desired.Trigger.Type == model.TriggerTypePoll && d.isPollTimedOut(sched) {
		log.Printf("schedule %s (%s): completing after timeout",
			sched.desired.ID, sched.desired.Name)
		d.completeSchedule(sched, model.CompletionTimeout)
		return
	}

	now := time.Now()
	raw := sched.trigger.NextFireTime(now)
	next, deferred := sched.window.Clamp(raw)

	// If the next natural slot fell outside the active-hours window, it was
	// deferred to the window opening. Record it as a distinct "suppressed" run
	// so the operator can see the fire was held back by active hours rather than
	// having silently vanished — scheduled_for is the slot we skipped.
	if deferred {
		d.logSuppressed(sched, raw, next)
	}

	d.mu.Lock()
	sched.runtime.NextFireAt = next
	d.mu.Unlock()

	if err := d.runtimeStore.Write(sched.runtime); err != nil {
		log.Printf("WARN: schedule %s: failed to persist runtime after fire: %v",
			sched.desired.ID, err)
	}
}

// logSuppressed appends a "suppressed" run-log entry recording that the fire due
// at scheduledFor was held back by the active-hours window and deferred to
// deferredTo. It is best-effort — a failed append is logged but never blocks the
// schedule from advancing.
func (d *Daemon) logSuppressed(sched *activeSchedule, scheduledFor, deferredTo time.Time) {
	window := sched.window.Describe()
	log.Printf("schedule %s (%s): fire at %s outside active hours %s, deferred to %s",
		sched.desired.ID, sched.desired.Name,
		scheduledFor.Format(time.RFC3339), window,
		deferredTo.Format(time.RFC3339))

	entry := &model.RunLogEntry{
		Timestamp:    time.Now(),
		Type:         model.LogTypeSuppressed,
		ScheduleID:   sched.desired.ID,
		Reason:       fmt.Sprintf("outside active hours %s (deferred to %s)", window, deferredTo.Format(time.RFC3339)),
		ScheduledFor: &scheduledFor,
	}
	if err := d.runLogStore.Append(entry); err != nil {
		log.Printf("WARN: schedule %s: failed to append suppressed entry: %v",
			sched.desired.ID, err)
	}
}

// suppressFire handles a fire that the active-hours gate held back: it records a
// "suppressed" run-log entry and defers NextFireAt to the next window opening at
// or after the intended slot. The command did not run and FireCount is
// unchanged. This applies uniformly to every trigger type — including a one-off
// `at`, which then fires (once) when the window opens rather than being dropped.
//
// This is the safety-net counterpart to advanceSchedule's clamp: NextFireAt is
// normally clamped into the window before the fire, so this path is reached only
// for a within-grace missed fire or a window that shifted between advance and
// fire. Caller (fireDueSchedules) holds sched.mu across the whole cycle.
func (d *Daemon) suppressFire(sched *activeSchedule, intendedFire time.Time) {
	deferredTo := sched.window.NextOpen(intendedFire)
	d.logSuppressed(sched, intendedFire, deferredTo)

	d.mu.Lock()
	sched.runtime.NextFireAt = deferredTo
	d.mu.Unlock()
	if err := d.runtimeStore.Write(sched.runtime); err != nil {
		log.Printf("WARN: schedule %s: failed to persist runtime after suppression: %v",
			sched.desired.ID, err)
	}
}

// deferPrecondition arranges what happens after a fire was held back by a
// failing precondition. It either schedules a backoff retry (advancing
// NextFireAt to now+backoff and bumping the attempt counter) or, when the retry
// would cross the give-up deadline, logs a miss, resets the retry state, and
// either advances to the next regular fire (recurring triggers) or completes the
// schedule (one-off `at`/`--once` with no recurrence).
//
// intendedFire is the fire time we were servicing — used as the miss's
// scheduled_for and, for recurring triggers, as the boundary the backoff must
// not cross.
//
// Termination precedence (give up if ANY holds):
//  1. A finite next regular fire exists and the next retry would land at/after it.
//  2. PreconditionMaxElapsed is set and now >= first-deferred-at + max-elapsed.
//  3. No finite next regular fire (one-off `at`) and now >= first-deferred-at +
//     the daemon at-deadline budget (DefaultPreconditionMaxElapsed / config).
//
// Caller (fireDueSchedules) holds sched.mu across the whole fire cycle, so the
// store writes here cannot race a concurrent RPC mutator of the same schedule.
func (d *Daemon) deferPrecondition(sched *activeSchedule, intendedFire time.Time) {
	now := time.Now()

	// Anchor the give-up budget and the original intended fire on the first
	// deferral of this retry sequence. intendedFire is captured fresh from
	// NextFireAt each tick, so on a backoff RETRY it is the retry instant, not the
	// scheduled occurrence — pin the real occurrence here on attempt #1.
	if sched.runtime.PreconditionFirstDeferredAt == nil {
		first := now
		sched.runtime.PreconditionFirstDeferredAt = &first
		origFire := intendedFire
		sched.runtime.PreconditionIntendedFire = &origFire
	}

	backoff := d.effectivePreconditionBackoff(sched)
	wait := backoff[min(sched.runtime.PreconditionAttempts, len(backoff)-1)]
	nextRetry := now.Add(wait)

	// Next regular fire (zero for a one-off `at` whose single fire has passed),
	// clamped to the active-hours window so the give-up bound and the eventual
	// advance both target the next window-eligible slot.
	nextRegularFire, _ := sched.nextFire(now)
	hasRegularFire := !nextRegularFire.IsZero()

	// Evaluate the give-up conditions.
	giveUp := false
	switch {
	case hasRegularFire && !nextRetry.Before(nextRegularFire):
		// The next retry would land at/after the next regular fire — stop and let
		// the regular schedule take over.
		giveUp = true
	default:
		// Elapsed-budget bound: an explicit per-schedule max-elapsed always
		// applies; otherwise the daemon at-budget applies only when there is no
		// finite next regular fire (one-off `at`).
		if budget, ok := d.preconditionElapsedBudget(sched, hasRegularFire); ok {
			if !now.Before(sched.runtime.PreconditionFirstDeferredAt.Add(budget)) {
				giveUp = true
			}
		}
	}

	if giveUp {
		log.Printf("schedule %s (%s): precondition not met before give-up deadline (%d attempt(s)), logging miss",
			sched.desired.ID, sched.desired.Name, sched.runtime.PreconditionAttempts)

		// Prefer the pinned original occurrence; fall back to the per-tick
		// intendedFire if (defensively) the anchor was never set.
		scheduledFor := intendedFire
		if sched.runtime.PreconditionIntendedFire != nil {
			scheduledFor = *sched.runtime.PreconditionIntendedFire
		}
		entry := &model.RunLogEntry{
			Timestamp:    now,
			Type:         model.LogTypeMiss,
			ScheduleID:   sched.desired.ID,
			Reason:       "precondition not met",
			ScheduledFor: &scheduledFor,
		}
		if err := d.runLogStore.Append(entry); err != nil {
			log.Printf("WARN: schedule %s: failed to append precondition miss entry: %v",
				sched.desired.ID, err)
		}

		// Reset retry state before advancing/completing so a future fire starts clean.
		sched.runtime.PreconditionAttempts = 0
		sched.runtime.PreconditionFirstDeferredAt = nil
		sched.runtime.PreconditionIntendedFire = nil

		if !hasRegularFire {
			// One-off `at` (or any trigger with no future fire): nothing to advance
			// to, so the schedule is done. completeSchedule persists and removes it.
			d.completeSchedule(sched, model.CompletionMissed)
			return
		}

		d.mu.Lock()
		sched.runtime.NextFireAt = nextRegularFire
		d.mu.Unlock()
		if err := d.runtimeStore.Write(sched.runtime); err != nil {
			log.Printf("WARN: schedule %s: failed to persist runtime after precondition give-up: %v",
				sched.desired.ID, err)
		}
		return
	}

	// Schedule a backoff retry. Bump the attempt counter (indexes the backoff
	// schedule) and set NextFireAt to the retry time. The regular schedule is NOT
	// advanced — once the precondition passes, the deferred fire still happens.
	sched.runtime.PreconditionAttempts++

	log.Printf("schedule %s (%s): precondition deferred fire (attempt #%d), retrying in %s",
		sched.desired.ID, sched.desired.Name, sched.runtime.PreconditionAttempts, wait)

	entry := &model.RunLogEntry{
		Timestamp:  now,
		Type:       model.LogTypeDeferred,
		ScheduleID: sched.desired.ID,
		Reason:     fmt.Sprintf("precondition not met (attempt #%d, retry in %s)", sched.runtime.PreconditionAttempts, wait),
	}
	if err := d.runLogStore.Append(entry); err != nil {
		log.Printf("WARN: schedule %s: failed to append deferred entry: %v",
			sched.desired.ID, err)
	}

	d.mu.Lock()
	sched.runtime.NextFireAt = nextRetry
	d.mu.Unlock()
	if err := d.runtimeStore.Write(sched.runtime); err != nil {
		log.Printf("WARN: schedule %s: failed to persist runtime after precondition defer: %v",
			sched.desired.ID, err)
	}
}

// effectivePreconditionBackoff resolves the backoff schedule for a single
// schedule: its per-schedule override if set and all entries parse, else the
// daemon default. Always returns a non-empty slice of positive durations so the
// caller can index it unconditionally — a malformed override degrades to the
// daemon default rather than aborting the retry.
func (d *Daemon) effectivePreconditionBackoff(sched *activeSchedule) []time.Duration {
	if parsed, ok := parseDurations(sched.desired.PreconditionBackoff); ok {
		return parsed
	}
	if len(sched.desired.PreconditionBackoff) > 0 {
		log.Printf("WARN: schedule %s: invalid precondition_backoff %v, using daemon default",
			sched.desired.ID, sched.desired.PreconditionBackoff)
	}
	if parsed, ok := parseDurations(d.cfg.Daemon.PreconditionBackoff); ok {
		return parsed
	}
	// Final fallback: the package default. Reached only if the daemon config was
	// constructed without applyDefaults (e.g. some tests) and carries a malformed
	// or empty backoff list.
	parsed, _ := parseDurations(model.DefaultPreconditionBackoff)
	return parsed
}

// preconditionElapsedBudget returns the elapsed-budget duration that bounds
// precondition retries, and whether such a budget applies.
//
//   - A per-schedule PreconditionMaxElapsed override always applies (and takes
//     precedence), regardless of whether a next regular fire exists.
//   - Otherwise the daemon at-deadline budget applies ONLY when there is no
//     finite next regular fire (a one-off `at`), since recurring triggers are
//     already bounded by their next fire.
func (d *Daemon) preconditionElapsedBudget(sched *activeSchedule, hasRegularFire bool) (time.Duration, bool) {
	if sched.desired.PreconditionMaxElapsed != "" {
		if dur, err := time.ParseDuration(sched.desired.PreconditionMaxElapsed); err == nil && dur > 0 {
			return dur, true
		}
		log.Printf("WARN: schedule %s: invalid precondition_max_elapsed %q, ignoring",
			sched.desired.ID, sched.desired.PreconditionMaxElapsed)
	}
	if hasRegularFire {
		return 0, false
	}
	budget := d.cfg.Daemon.PreconditionMaxElapsed
	if budget == "" {
		budget = model.DefaultPreconditionMaxElapsed
	}
	if dur, err := time.ParseDuration(budget); err == nil && dur > 0 {
		return dur, true
	}
	// Misconfigured daemon budget — fall back to the package default so an `at`
	// precondition still has a finite give-up deadline.
	dur, _ := time.ParseDuration(model.DefaultPreconditionMaxElapsed)
	return dur, true
}

// parseDurations parses a slice of Go-duration strings into time.Durations.
// Returns (nil, false) if the slice is empty or any entry fails to parse or is
// non-positive, so callers can fall back to a default.
func parseDurations(in []string) ([]time.Duration, bool) {
	if len(in) == 0 {
		return nil, false
	}
	out := make([]time.Duration, 0, len(in))
	for _, s := range in {
		dur, err := time.ParseDuration(s)
		if err != nil || dur <= 0 {
			return nil, false
		}
		out = append(out, dur)
	}
	return out, true
}

// completeSchedule marks a schedule as completed with the given reason: updates
// desired state in the data repo, git commits, deletes runtime state, and
// removes from active schedules.
func (d *Daemon) completeSchedule(sched *activeSchedule, reason model.CompletionReason) {
	id := sched.desired.ID
	name := sched.desired.Name

	log.Printf("schedule %s (%s): once schedule completed after %d fire(s)",
		id, name, sched.runtime.FireCount)

	// Update desired state to completed.
	sched.desired.Status = model.StatusCompleted
	sched.desired.CompletionReason = reason
	if err := d.desiredStore.Write(sched.desired); err != nil {
		log.Printf("WARN: schedule %s: failed to write completed state: %v", id, err)
		return
	}

	// Git commit.
	filePath := d.desiredStore.FilePath(id)
	commitMsg := fmt.Sprintf("sundial: complete schedule %s (%s)", id, name)
	if err := d.gitOps.CommitSchedule(filePath, commitMsg); err != nil {
		log.Printf("WARN: schedule %s: failed to git commit completion: %v", id, err)
	}

	// Best-effort push.
	if err := d.gitOps.Push(); err != nil {
		log.Printf("WARN: schedule %s: git push failed after completion: %v", id, err)
	}

	// Delete runtime state.
	if err := d.runtimeStore.Delete(id); err != nil {
		log.Printf("WARN: schedule %s: failed to delete runtime state: %v", id, err)
	}

	// Remove from active schedules.
	d.mu.Lock()
	delete(d.schedules, id)
	d.mu.Unlock()
}
