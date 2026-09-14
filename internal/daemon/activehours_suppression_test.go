package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/fyang0507/sundial/internal/model"
)

func TestFireDueSchedules_StaleActiveHoursSuppression(t *testing.T) {
	tests := []struct {
		name, window, pending, now, opening string
	}{
		{
			name: "overdue readiness retry", window: "08:00-21:30",
			pending: "2026-09-13T20:44:12.274557-04:00", now: "2026-09-13T21:59:35.331370-04:00",
			opening: "2026-09-14T08:00:00-04:00",
		},
		{
			name: "excluded closing boundary", window: "08:00-21:30",
			pending: "2026-09-13T20:44:12.274557-04:00", now: "2026-09-13T21:30:00-04:00",
			opening: "2026-09-14T08:00:00-04:00",
		},
		{
			name: "overnight window", window: "22:00-02:00",
			pending: "2026-09-14T01:00:00-04:00", now: "2026-09-14T03:00:00-04:00",
			opening: "2026-09-14T22:00:00-04:00",
		},
		{
			name: "spring DST crossing", window: "08:00-21:30",
			pending: "2026-03-07T20:44:12-05:00", now: "2026-03-07T23:00:00-05:00",
			opening: "2026-03-08T08:00:00-04:00",
		},
		{
			name: "fall DST crossing", window: "08:00-21:30",
			pending: "2026-10-31T20:44:12-04:00", now: "2026-10-31T23:00:00-04:00",
			opening: "2026-11-01T08:00:00-05:00",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				now := suppressionTime(t, tt.now)
				pending := suppressionTime(t, tt.pending)
				opening := suppressionTime(t, tt.opening)
				time.Sleep(now.Sub(time.Now()))

				d := newTestDaemon(t)
				var err error
				d.activeHours, err = model.ParseActiveHours(tt.window, "America/New_York")
				if err != nil {
					t.Fatal(err)
				}
				desired := makeCronDesired("sch_stale_window", "stale window", "* * * * *")
				commandMarker := filepath.Join(t.TempDir(), "command")
				preconditionMarker := filepath.Join(t.TempDir(), "precondition")
				desired.Command = ""
				desired.CommandArgs = suppressionMarkerCommand(commandMarker)
				desired.PreconditionArgs = suppressionMarkerCommand(preconditionMarker)
				sched := buildSched(t, d, desired)
				sched.runtime.NextFireAt = pending
				d.schedules[desired.ID] = sched

				// Keep NextOpen's explicit-input contract: the stale pending slot
				// is eligible in isolation, even though the actual clock is closed.
				if got := sched.window.NextOpen(pending); !got.Equal(pending) {
					t.Fatalf("NextOpen(pending) = %s, want unchanged %s", got, pending)
				}
				if sched.window.Contains(now) {
					t.Fatal("test service time must be outside active hours")
				}

				d.fireDueSchedules()
				d.wg.Wait()
				if got := sched.runtime.NextFireAt; !got.Equal(opening) || !got.After(now) {
					t.Fatalf("NextFireAt = %s, want future opening %s", got, opening)
				}
				persisted, err := d.runtimeStore.Read(desired.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !persisted.NextFireAt.Equal(opening) {
					t.Fatalf("persisted NextFireAt = %s, want %s", persisted.NextFireAt, opening)
				}

				// Drive repeated ticks, including the final nanosecond before open.
				// synctest advances the clock instantly without waiting overnight.
				for _, tick := range []time.Time{now, now.Add(time.Minute), opening.Add(-time.Nanosecond)} {
					time.Sleep(tick.Sub(time.Now()))
					d.fireDueSchedules()
					d.wg.Wait()
				}
				entries, err := d.runLogStore.Read(desired.ID)
				if err != nil {
					t.Fatal(err)
				}
				if len(entries) != 1 || entries[0].Type != model.LogTypeSuppressed {
					t.Fatalf("want exactly one suppression after repeated ticks, got %+v", entries)
				}
				entry := entries[0]
				if !entry.Timestamp.Equal(now) || entry.ScheduledFor == nil || !entry.ScheduledFor.Equal(pending) {
					t.Errorf("suppression timestamp/scheduled_for = %s/%v, want %s/%s", entry.Timestamp, entry.ScheduledFor, now, pending)
				}
				if !strings.Contains(entry.Reason, "deferred to "+tt.opening) {
					t.Errorf("suppression reason does not name the opening: %s", entry.Reason)
				}
				if sched.runtime.FireCount != 0 || sched.runtime.LastFiredAt != nil || sched.runtime.PreconditionAttempts != 0 {
					t.Errorf("suppressed fire changed execution/readiness state: %+v", sched.runtime)
				}
				for _, marker := range []string{commandMarker, preconditionMarker} {
					if _, err := os.Stat(marker); !os.IsNotExist(err) {
						t.Errorf("command/precondition must not run before opening: marker %s, err %v", marker, err)
					}
				}
			})
		})
	}
}

func TestFireDueSchedules_ActiveHoursEligible(t *testing.T) {
	for _, tt := range []struct {
		name, now string
		ignore    bool
	}{
		{name: "inside window", now: "2026-09-14T11:00:00-04:00"},
		{name: "included opening boundary", now: "2026-09-14T08:00:00-04:00"},
		{name: "opt out outside window", now: "2026-09-13T21:59:35.331370-04:00", ignore: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				now := suppressionTime(t, tt.now)
				time.Sleep(now.Sub(time.Now()))
				d := newTestDaemon(t)
				d.activeHours = &model.ActiveHours{Start: "08:00", End: "21:30", Timezone: "America/New_York"}
				desired := makeCronDesired("sch_eligible_window", "eligible window", "* * * * *")
				marker := filepath.Join(t.TempDir(), "invocations")
				desired.Command = ""
				desired.CommandArgs = suppressionMarkerCommand(marker)
				desired.IgnoreActiveHours = tt.ignore
				sched := buildSched(t, d, desired)
				sched.runtime.NextFireAt = now
				d.schedules[desired.ID] = sched

				for range 3 {
					d.fireDueSchedules()
					d.wg.Wait()
				}
				entries, err := d.runLogStore.Read(desired.ID)
				if err != nil {
					t.Fatal(err)
				}
				if len(entries) != 1 || entries[0].Type != model.LogTypeFire || sched.runtime.FireCount != 1 {
					t.Fatalf("want one command fire without suppression, got entries %+v, count %d", entries, sched.runtime.FireCount)
				}
				if data, err := os.ReadFile(marker); err != nil || string(data) != "called\n" {
					t.Errorf("command invocations = %q, err %v, want one", data, err)
				}
				if !sched.runtime.NextFireAt.After(now) {
					t.Errorf("eligible fire did not advance: %s", sched.runtime.NextFireAt)
				}
			})
		})
	}
}

func suppressionTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func suppressionMarkerCommand(path string) []string {
	return []string{"/bin/sh", "-c", `printf 'called\n' >> "$1"`, "marker", path}
}
