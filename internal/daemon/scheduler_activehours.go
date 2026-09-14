package daemon

import (
	"fmt"
	"log"
	"time"

	"github.com/fyang0507/sundial/internal/model"
)

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
// or after the current suppression time. The pending intended slot is retained
// only as scheduled_for; it may be an in-window retry now overdue. The command
// did not run, so FireCount and readiness retry history are unchanged. This
// applies uniformly to every trigger type, including a one-off `at`.
//
// This is the safety-net counterpart to advanceSchedule's clamp: NextFireAt is
// normally clamped into the window before the fire, so this path is reached only
// for a delayed fire or a window that shifted between advance and fire.
// Caller (fireDueSchedules) holds sched.mu across the whole cycle.
func (d *Daemon) suppressFire(sched *activeSchedule, intendedFire time.Time) {
	deferredTo := sched.window.NextOpen(time.Now())
	d.logSuppressed(sched, intendedFire, deferredTo)

	d.mu.Lock()
	sched.runtime.NextFireAt = deferredTo
	d.mu.Unlock()
	if err := d.runtimeStore.Write(sched.runtime); err != nil {
		log.Printf("WARN: schedule %s: failed to persist runtime after suppression: %v",
			sched.desired.ID, err)
	}
}
