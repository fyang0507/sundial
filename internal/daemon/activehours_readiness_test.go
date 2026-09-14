package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/fyang0507/sundial/internal/model"
)

func TestActiveHoursSuppression_ReadinessContinuity(t *testing.T) {
	for _, tc := range []struct {
		name       string
		at         bool
		maxElapsed string
		ready      bool
		retry      bool
	}{
		{name: "expired default at budget", at: true},
		{name: "expired explicit recurring budget", maxElapsed: "24h"},
		{name: "ready despite expired recurring budget", maxElapsed: "24h", ready: true},
		{name: "ready despite expired default at budget", at: true, ready: true},
		{name: "remaining recurring budget", maxElapsed: "72h", retry: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				parse := func(value string) time.Time {
					t.Helper()
					parsed, err := time.Parse(time.RFC3339Nano, value)
					if err != nil {
						t.Fatal(err)
					}
					return parsed
				}
				original := parse("2026-09-12T10:00:00-04:00")
				firstDeferred := parse("2026-09-12T10:00:05-04:00")
				pending := parse("2026-09-13T20:44:12.274557-04:00")
				suppressedAt := parse("2026-09-13T21:59:35.331370-04:00")
				opening := parse("2026-09-14T08:00:00-04:00")
				time.Sleep(suppressedAt.Sub(time.Now()))

				d := newTestDaemon(t)
				d.activeHours = &model.ActiveHours{
					Start: "08:00", End: "21:30", Timezone: "America/New_York",
				}
				desired := makeCronDesired("sch_readiness", "readiness continuity", "CRON_TZ=America/New_York 0 10 * * *")
				if tc.at {
					desired = makeAtDesired(desired.ID, desired.Name, original)
				}
				markers := t.TempDir()
				preconditionMarker := filepath.Join(markers, "precondition")
				commandMarker := filepath.Join(markers, "command")
				exitCode := "1"
				if tc.ready {
					exitCode = "0"
				}
				desired.Command = ""
				desired.CommandArgs = []string{"/bin/sh", "-c", `printf x >> "$1"`, "marker", commandMarker}
				desired.PreconditionArgs = []string{"/bin/sh", "-c", `printf x >> "$1"; exit "$2"`, "marker", preconditionMarker, exitCode}
				desired.PreconditionMaxElapsed = tc.maxElapsed
				// A continued sequence uses the capped 20m backoff, whereas a
				// mistakenly reset sequence would use the first 1m backoff.
				desired.PreconditionBackoff = []string{"1m", "5m", "20m"}
				sched := buildSched(t, d, desired)
				*sched.runtime = model.RuntimeState{
					ID:                          desired.ID,
					NextFireAt:                  pending,
					PreconditionAttempts:        10,
					PreconditionFirstDeferredAt: &firstDeferred,
					PreconditionIntendedFire:    &original,
				}
				if err := d.desiredStore.Write(desired); err != nil {
					t.Fatal(err)
				}
				if err := d.runtimeStore.Write(sched.runtime); err != nil {
					t.Fatal(err)
				}
				d.schedules[desired.ID] = sched
				want := *sched.runtime
				want.NextFireAt = opening

				// Drive the outer gate and its rescheduling path, then revisit
				// the scheduler throughout the closed window.
				for _, tick := range []time.Time{suppressedAt, suppressedAt.Add(time.Minute), opening.Add(-time.Nanosecond)} {
					time.Sleep(tick.Sub(time.Now()))
					d.fireDueSchedules()
					d.wg.Wait()
					assertReadinessRuntime(t, d, sched, want)
					assertReadinessMarker(t, preconditionMarker, "")
					assertReadinessMarker(t, commandMarker, "")
					entries := readinessEntries(t, d, desired.ID)
					if len(entries) != 1 || entries[0].Type != model.LogTypeSuppressed {
						t.Fatalf("before opening: want one suppression, got %+v", entries)
					}
					if entries[0].ScheduledFor == nil || !entries[0].ScheduledFor.Equal(pending) {
						t.Fatalf("suppression scheduled_for = %v, want pending retry %s", entries[0].ScheduledFor, pending)
					}
				}

				time.Sleep(opening.Sub(time.Now()))
				d.fireDueSchedules()
				d.wg.Wait()
				// Another tick at the opening must neither recheck readiness
				// nor rerun the command for this pending fire.
				d.fireDueSchedules()
				d.wg.Wait()
				assertReadinessMarker(t, preconditionMarker, "x")
				commandContents := ""
				if tc.ready {
					commandContents = "x"
				}
				assertReadinessMarker(t, commandMarker, commandContents)

				entries := readinessEntries(t, d, desired.ID)
				if len(entries) != 2 {
					t.Fatalf("after opening: want suppression and one outcome, got %+v", entries)
				}
				wantType := model.LogTypeMiss
				if tc.ready {
					wantType = model.LogTypeFire
				} else if tc.retry {
					wantType = model.LogTypeDeferred
				}
				if entries[1].Type != wantType {
					t.Fatalf("opening outcome = %s, want %s", entries[1].Type, wantType)
				}
				if wantType == model.LogTypeMiss {
					if entries[1].ScheduledFor == nil || !entries[1].ScheduledFor.Equal(original) {
						t.Errorf("miss scheduled_for = %v, want original occurrence %s", entries[1].ScheduledFor, original)
					}
				}
				if tc.retry {
					want.PreconditionAttempts++
					want.NextFireAt = opening.Add(20 * time.Minute)
				} else {
					want.PreconditionAttempts = 0
					want.PreconditionFirstDeferredAt = nil
					want.PreconditionIntendedFire = nil
					want.NextFireAt = parse("2026-09-14T10:00:00-04:00")
					if tc.ready {
						want.FireCount = 1
					}
				}
				if !tc.at {
					assertReadinessRuntime(t, d, sched, want)
					return
				}

				if sched.runtime.PreconditionAttempts != 0 || sched.runtime.PreconditionFirstDeferredAt != nil || sched.runtime.PreconditionIntendedFire != nil {
					t.Errorf("completed one-off retained readiness history: %+v", sched.runtime)
				}
				if sched.runtime.FireCount != want.FireCount {
					t.Errorf("FireCount = %d, want %d", sched.runtime.FireCount, want.FireCount)
				}
				if _, active := d.schedules[desired.ID]; active {
					t.Error("one-off remained active after its opening outcome")
				}
				if _, err := d.runtimeStore.Read(desired.ID); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("completed one-off runtime should be removed, got %v", err)
				}
				completed, err := d.desiredStore.Read(desired.ID)
				if err != nil {
					t.Fatal(err)
				}
				wantReason := model.CompletionMissed
				if tc.ready {
					wantReason = model.CompletionTriggered
				}
				if completed.Status != model.StatusCompleted || completed.CompletionReason != wantReason {
					t.Errorf("one-off completion = %s/%s, want completed/%s", completed.Status, completed.CompletionReason, wantReason)
				}
			})
		})
	}
}

func assertReadinessRuntime(t *testing.T, d *Daemon, sched *activeSchedule, want model.RuntimeState) {
	t.Helper()
	persisted, err := d.runtimeStore.Read(sched.desired.ID)
	if err != nil {
		t.Fatal(err)
	}
	for source, got := range map[string]*model.RuntimeState{"memory": sched.runtime, "store": persisted} {
		if !got.NextFireAt.Equal(want.NextFireAt) || got.PreconditionAttempts != want.PreconditionAttempts || got.FireCount != want.FireCount {
			t.Errorf("%s runtime next/attempts/fires = %s/%d/%d, want %s/%d/%d", source, got.NextFireAt, got.PreconditionAttempts, got.FireCount, want.NextFireAt, want.PreconditionAttempts, want.FireCount)
		}
		for _, anchor := range []struct {
			name      string
			got, want *time.Time
		}{
			{"first deferral", got.PreconditionFirstDeferredAt, want.PreconditionFirstDeferredAt},
			{"original occurrence", got.PreconditionIntendedFire, want.PreconditionIntendedFire},
		} {
			if (anchor.got == nil) != (anchor.want == nil) || (anchor.got != nil && anchor.want != nil && !anchor.got.Equal(*anchor.want)) {
				t.Errorf("%s %s = %v, want %v", source, anchor.name, anchor.got, anchor.want)
			}
		}
	}
}

func assertReadinessMarker(t *testing.T, path, want string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if want == "" && errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != want {
		t.Errorf("marker %s = %q, want %q", path, contents, want)
	}
}

func readinessEntries(t *testing.T, d *Daemon, id string) []*model.RunLogEntry {
	t.Helper()
	entries, err := d.runLogStore.Read(id)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}
