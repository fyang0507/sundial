package daemon

import (
	"log"
	"time"
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
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			d.execute(sched)
			d.advanceSchedule(sched)
			d.signalWake()
		}()
	}
}

// advanceSchedule recomputes the next fire time for a single schedule
// and persists the updated runtime state.
func (d *Daemon) advanceSchedule(sched *activeSchedule) {
	next := sched.trigger.NextFireTime(time.Now())

	d.mu.Lock()
	sched.runtime.NextFireAt = next
	d.mu.Unlock()

	if err := d.runtimeStore.Write(sched.runtime); err != nil {
		log.Printf("WARN: schedule %s: failed to persist runtime after fire: %v",
			sched.desired.ID, err)
	}
}
