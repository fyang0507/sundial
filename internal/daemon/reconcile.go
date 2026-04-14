package daemon

import (
	"log"
	"time"

	"github.com/fyang0507/sundial/internal/model"
	"github.com/fyang0507/sundial/internal/trigger"
)

// missGracePeriod is the window within which a missed fire is still executed.
const missGracePeriod = 60 * time.Second

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
		case model.StatusActive:
			// Parse trigger.
			trig, err := trigger.ParseTrigger(ds.Trigger)
			if err != nil {
				log.Printf("WARN: schedule %s (%s): failed to parse trigger: %v", id, ds.Name, err)
				continue
			}

			if rs == nil {
				// Active desired + no runtime -> create runtime.
				nextFire := trig.NextFireTime(time.Now())
				rs = &model.RuntimeState{
					ID:         id,
					NextFireAt: nextFire,
				}
				if err := d.runtimeStore.Write(rs); err != nil {
					log.Printf("WARN: schedule %s: failed to write runtime state: %v", id, err)
					continue
				}
			}

			d.schedules[id] = &activeSchedule{
				desired: ds,
				runtime: rs,
				trigger: trig,
			}
			activeIDs[id] = struct{}{}

		case model.StatusRemoved:
			// Removed desired + runtime exists -> delete runtime.
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
		d.handleMissedFires()
	}

	d.advanceAllSchedules()
	d.signalWake()

	return nil
}

// handleMissedFires checks each active schedule for fires that should have
// occurred while the daemon was not running.
func (d *Daemon) handleMissedFires() {
	now := time.Now()

	for id, sched := range d.schedules {
		nextFire := sched.runtime.NextFireAt
		if nextFire.IsZero() || !nextFire.Before(now) {
			continue
		}

		elapsed := now.Sub(nextFire)

		if elapsed <= missGracePeriod {
			// Within grace period: fire the command.
			log.Printf("schedule %s (%s): missed fire within grace period (%.0fs ago), executing",
				id, sched.desired.Name, elapsed.Seconds())
			// Execute in current goroutine during startup.
			d.execute(sched)
		} else {
			// Beyond grace period: log misses.
			d.logMissedFires(sched, nextFire, now)
		}
	}
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
func (d *Daemon) advanceAllSchedules() {
	now := time.Now()
	for id, sched := range d.schedules {
		next := sched.trigger.NextFireTime(now)
		sched.runtime.NextFireAt = next
		if err := d.runtimeStore.Write(sched.runtime); err != nil {
			log.Printf("WARN: schedule %s: failed to persist runtime state: %v", id, err)
		}
	}
}
