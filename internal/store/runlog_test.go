package store

import (
	"testing"
	"time"

	"github.com/fyang0507/sundial/internal/model"
)

func makeFireEntry(scheduleID string, ts time.Time) *model.RunLogEntry {
	exitCode := 0
	dur := 1.5
	return &model.RunLogEntry{
		Timestamp:     ts,
		Type:          model.LogTypeFire,
		ScheduleID:    scheduleID,
		ExitCode:      &exitCode,
		DurationSec:   &dur,
		StdoutPreview: "hello",
	}
}

func makeMissEntry(scheduleID string, ts time.Time) *model.RunLogEntry {
	scheduledFor := ts.Add(-time.Minute)
	return &model.RunLogEntry{
		Timestamp:    ts,
		Type:         model.LogTypeMiss,
		ScheduleID:   scheduleID,
		Reason:       "daemon was not running",
		ScheduledFor: &scheduledFor,
	}
}

func makeMissSummaryEntry(scheduleID string, ts time.Time, count int) *model.RunLogEntry {
	return &model.RunLogEntry{
		Timestamp:  ts,
		Type:       model.LogTypeMissSummary,
		ScheduleID: scheduleID,
		Count:      count,
		From:       "2025-01-10T09:00:00Z",
		To:         "2025-01-14T09:00:00Z",
	}
}

func TestRunLogStore_AppendAndRead(t *testing.T) {
	dir := t.TempDir()
	s := NewRunLogStore(dir)
	if err := s.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	id := "sch_log1"
	ts1 := time.Date(2025, 1, 15, 9, 0, 0, 0, time.UTC)
	ts2 := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	ts3 := time.Date(2025, 1, 15, 11, 0, 0, 0, time.UTC)

	entries := []*model.RunLogEntry{
		makeFireEntry(id, ts1),
		makeMissEntry(id, ts2),
		makeFireEntry(id, ts3),
	}

	for i, e := range entries {
		if err := s.Append(e); err != nil {
			t.Fatalf("Append entry %d: %v", i, err)
		}
	}

	got, err := s.Read(id)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("Read returned %d entries, want 3", len(got))
	}

	// Check order is preserved
	if got[0].Type != model.LogTypeFire {
		t.Errorf("entry[0].Type = %q, want %q", got[0].Type, model.LogTypeFire)
	}
	if got[1].Type != model.LogTypeMiss {
		t.Errorf("entry[1].Type = %q, want %q", got[1].Type, model.LogTypeMiss)
	}
	if got[2].Type != model.LogTypeFire {
		t.Errorf("entry[2].Type = %q, want %q", got[2].Type, model.LogTypeFire)
	}

	// Verify round-trip of fields
	if got[0].ExitCode == nil || *got[0].ExitCode != 0 {
		t.Errorf("entry[0].ExitCode = %v, want 0", got[0].ExitCode)
	}
	if got[0].StdoutPreview != "hello" {
		t.Errorf("entry[0].StdoutPreview = %q, want %q", got[0].StdoutPreview, "hello")
	}
	if got[1].Reason != "daemon was not running" {
		t.Errorf("entry[1].Reason = %q, want %q", got[1].Reason, "daemon was not running")
	}
}

func TestRunLogStore_MissedSince(t *testing.T) {
	dir := t.TempDir()
	s := NewRunLogStore(dir)
	if err := s.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	id := "sch_miss"
	base := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)

	// Entries: fire, miss, miss_summary(3), miss — all after base
	entries := []*model.RunLogEntry{
		makeFireEntry(id, base.Add(1*time.Hour)),
		makeMissEntry(id, base.Add(2*time.Hour)),
		makeMissSummaryEntry(id, base.Add(3*time.Hour), 3),
		makeMissEntry(id, base.Add(4*time.Hour)),
	}

	for _, e := range entries {
		if err := s.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	// All entries after base: 1 miss + 3 (summary) + 1 miss = 5
	count, err := s.MissedSince(id, base)
	if err != nil {
		t.Fatalf("MissedSince: %v", err)
	}
	if count != 5 {
		t.Errorf("MissedSince(base) = %d, want 5", count)
	}

	// Only entries after 2.5h mark: summary(3) + miss(1) = 4
	since := base.Add(2*time.Hour + 30*time.Minute)
	count, err = s.MissedSince(id, since)
	if err != nil {
		t.Fatalf("MissedSince: %v", err)
	}
	if count != 4 {
		t.Errorf("MissedSince(+2.5h) = %d, want 4", count)
	}
}

func TestRunLogStore_ReadEmpty(t *testing.T) {
	dir := t.TempDir()
	s := NewRunLogStore(dir)
	if err := s.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	// Read non-existent file should return empty slice
	got, err := s.Read("sch_nonexistent")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Read returned %d entries, want 0", len(got))
	}
}

func TestRunLogStore_MissedSinceEmpty(t *testing.T) {
	dir := t.TempDir()
	s := NewRunLogStore(dir)
	if err := s.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	count, err := s.MissedSince("sch_nonexistent", time.Now())
	if err != nil {
		t.Fatalf("MissedSince: %v", err)
	}
	if count != 0 {
		t.Errorf("MissedSince = %d, want 0", count)
	}
}
