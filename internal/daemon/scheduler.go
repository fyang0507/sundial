package daemon

import (
	"fmt"
	"log"
	"time"

	"github.com/fyang0507/sundial/internal/model"
)

// runLoop is the main scheduler loop. It sleeps until the next fire time,
// fires due schedules, and recalculates when the schedule set changes.
func (d *Daemon) runLoop() {
	for {
		nextID, nextTime := d.soonestFire()

		var timer *time.Timer
		if nextID == "" {
			// No active schedules — use a long sleep and wait for wake/quit.
			timer = time.NewTimer(24 * time.Hour)
		} else {
			dur := time.Until(nextTime)
			if dur < 0 {
				dur = 0
			}
			timer = time.NewTimer(dur)
		}

		select {
		case <-timer.C:
			if nextID == "" {
				// Spurious timer, loop back.
				continue
			}
			d.fireDueSchedules()

		case <-d.wake:
			timer.Stop()
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
		sched.runtime.NextFireAt = time.Time{}
		d.mu.Unlock()
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			defer sched.mu.Unlock()
			d.execute(sched)
			d.advanceSchedule(sched)
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

	next := sched.trigger.NextFireTime(time.Now())

	d.mu.Lock()
	sched.runtime.NextFireAt = next
	d.mu.Unlock()

	if err := d.runtimeStore.Write(sched.runtime); err != nil {
		log.Printf("WARN: schedule %s: failed to persist runtime after fire: %v",
			sched.desired.ID, err)
	}
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
