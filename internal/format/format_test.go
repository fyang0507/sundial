package format

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/fyang0507/sundial/internal/model"
)

// ---------- FormatAddResult ----------

func TestFormatAddResult_PlainText(t *testing.T) {
	r := &model.AddResult{
		ID:        "sch_a1b2c3",
		Name:      "Trash bin check",
		Schedule:  "Every Mon, Tue at 1h before sunset (San Francisco)",
		NextFire:  "2026-04-14 6:42pm PDT",
		Status:    "active",
		SavedTo:   "~/data_repo/sundial/schedules/sch_a1b2c3.json",
		Committed: `sundial: add schedule sch_a1b2c3 "Trash bin check"`,
	}
	want := `id: sch_a1b2c3
name: Trash bin check
schedule: Every Mon, Tue at 1h before sunset (San Francisco)
next_fire: 2026-04-14 6:42pm PDT
status: active
saved_to: ~/data_repo/sundial/schedules/sch_a1b2c3.json
committed: sundial: add schedule sch_a1b2c3 "Trash bin check"`

	got := FormatAddResult(r, false)
	if got != want {
		t.Errorf("FormatAddResult plain text mismatch.\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestFormatAddResult_WithRecoveryAndWarning(t *testing.T) {
	r := &model.AddResult{
		ID:        "sch_x9y8z7",
		Name:      "Test schedule",
		Schedule:  "daily at 9am",
		NextFire:  "2026-04-14 9:00am PDT",
		Status:    "active",
		SavedTo:   "~/data_repo/sundial/schedules/sch_x9y8z7.json",
		Committed: `sundial: add schedule sch_x9y8z7 "Test schedule"`,
		Recovery:  "recovered from stale state",
		Warning:   "command path does not exist",
	}
	got := FormatAddResult(r, false)

	// Verify recovery and warning lines are present.
	wantSuffix := "recovery: recovered from stale state\nwarning: command path does not exist"
	if got[len(got)-len(wantSuffix):] != wantSuffix {
		t.Errorf("expected recovery+warning lines at end, got:\n%s", got)
	}
}

func TestFormatAddResult_JSON(t *testing.T) {
	r := &model.AddResult{
		ID:     "sch_a1b2c3",
		Name:   "Trash bin check",
		Status: "active",
	}
	got := FormatAddResult(r, true)
	// Must be valid JSON.
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("FormatAddResult JSON is invalid: %v\nraw: %s", err, got)
	}
	if parsed["id"] != "sch_a1b2c3" {
		t.Errorf("expected id=sch_a1b2c3, got %v", parsed["id"])
	}
}

// ---------- FormatRemoveResult ----------

func TestFormatRemoveResult_SingleID(t *testing.T) {
	r := &model.RemoveResult{ID: "sch_a1b2c3", Removed: 1}
	got := FormatRemoveResult(r, false)
	want := "removed: sch_a1b2c3"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatRemoveResult_Multiple(t *testing.T) {
	r := &model.RemoveResult{Removed: 5}
	got := FormatRemoveResult(r, false)
	want := "removed: 5 schedules"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatRemoveResult_JSON(t *testing.T) {
	r := &model.RemoveResult{ID: "sch_a1b2c3", Removed: 1}
	got := FormatRemoveResult(r, true)
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed["id"] != "sch_a1b2c3" {
		t.Errorf("expected id sch_a1b2c3, got %v", parsed["id"])
	}
}

// ---------- FormatListResult ----------

func TestFormatListResult_Empty(t *testing.T) {
	r := &model.ListResult{Schedules: []model.ScheduleSummary{}}
	got := FormatListResult(r, false)
	want := "No schedules found."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatListResult_PlainText(t *testing.T) {
	r := &model.ListResult{
		Schedules: []model.ScheduleSummary{
			{
				ID:       "sch_a1b2c3",
				Name:     "Trash bin check",
				Schedule: "Every Mon, Tue at 1h before sunset (San Francisco)",
				NextFire: "2026-04-14 6:42pm PDT",
				Status:   "active",
			},
		},
	}
	got := FormatListResult(r, false)
	// Header should be present.
	if got[:2] != "ID" {
		t.Errorf("expected table header starting with 'ID', got:\n%s", got)
	}
	// Row data should be present.
	if !contains(got, "sch_a1b2c3") || !contains(got, "Trash bin check") || !contains(got, "active") {
		t.Errorf("expected schedule data in table, got:\n%s", got)
	}
	// Long schedule should be truncated.
	if contains(got, "(San Francisco)") {
		t.Errorf("expected schedule to be truncated, got:\n%s", got)
	}
}

func TestFormatListResult_JSON(t *testing.T) {
	r := &model.ListResult{Schedules: []model.ScheduleSummary{}}
	got := FormatListResult(r, true)
	var parsed model.ListResult
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

// ---------- FormatShowResult ----------

func TestFormatShowResult_PlainText(t *testing.T) {
	exitCode := 0
	missedSince := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
	r := &model.ShowResult{
		ScheduleSummary: model.ScheduleSummary{
			ID:           "sch_a1b2c3",
			Name:         "Trash bin check",
			Schedule:     "Every Mon, Tue at 1h before sunset (San Francisco)",
			NextFire:     "2026-04-14 6:42pm PDT",
			LastFire:     "2026-04-08 6:38pm PDT",
			LastExitCode: &exitCode,
			Status:       "active",
			MissedCount:  2,
			MissedSince:  &missedSince,
		},
		Command: "cd ~/projects/trash && codex exec '...'",
	}

	got := FormatShowResult(r, false)
	want := `id: sch_a1b2c3
name: Trash bin check
schedule: Every Mon, Tue at 1h before sunset (San Francisco)
next_fire: 2026-04-14 6:42pm PDT
last_fire: 2026-04-08 6:38pm PDT (exit 0)
missed: 2 since last fire (daemon offline since 2026-04-10)
status: active
command: cd ~/projects/trash && codex exec '...'`

	if got != want {
		t.Errorf("FormatShowResult mismatch.\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestFormatShowResult_NoMissedNoLastFire(t *testing.T) {
	r := &model.ShowResult{
		ScheduleSummary: model.ScheduleSummary{
			ID:       "sch_a1b2c3",
			Name:     "Simple",
			Schedule: "daily at 9am",
			NextFire: "2026-04-14 9:00am PDT",
			Status:   "active",
		},
		Command: "echo hello",
	}
	got := FormatShowResult(r, false)
	if contains(got, "last_fire") {
		t.Error("last_fire should be omitted when empty")
	}
	if contains(got, "missed") {
		t.Error("missed should be omitted when MissedCount == 0")
	}
}

func TestFormatShowResult_JSON(t *testing.T) {
	r := &model.ShowResult{
		ScheduleSummary: model.ScheduleSummary{
			ID:     "sch_abc",
			Status: "active",
		},
		Command: "echo hi",
	}
	got := FormatShowResult(r, true)
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

// ---------- FormatHealthResult ----------

func TestFormatHealthResult_PlainText(t *testing.T) {
	r := &model.HealthResult{
		Healthy:       true,
		ScheduleCount: 3,
		EffectivePath: "/usr/local/bin:/opt/homebrew/bin",
		Checks: []model.HealthCheck{
			{Name: "daemon", Status: "ok"},
			{Name: "config", Status: "ok"},
			{Name: "data_repo", Status: "ok"},
			{Name: "git_status", Status: "ok", Message: "no pending pushes"},
		},
		OrphanedSchedules: []string{"sch_abc123"},
	}

	got := FormatHealthResult(r, false)

	// Verify header.
	if !contains(got, "sundial health") {
		t.Error("expected 'sundial health' header")
	}
	// Check statuses appear.
	if !contains(got, "daemon: ok") {
		t.Error("expected 'daemon: ok'")
	}
	if !contains(got, "git_status: ok (no pending pushes)") {
		t.Error("expected git_status with message")
	}
	if !contains(got, "schedules: 3 active") {
		t.Error("expected schedule count")
	}
	// Warnings section.
	if !contains(got, "warnings:") {
		t.Error("expected warnings section")
	}
	if !contains(got, "1 orphaned schedule: sch_abc123") {
		t.Error("expected orphaned schedule warning")
	}
}

func TestFormatHealthResult_NoWarnings(t *testing.T) {
	r := &model.HealthResult{
		Healthy:       true,
		ScheduleCount: 0,
		Checks: []model.HealthCheck{
			{Name: "daemon", Status: "ok"},
		},
	}
	got := FormatHealthResult(r, false)
	if contains(got, "warnings:") {
		t.Error("should not have warnings section when none exist")
	}
}

func TestFormatHealthResult_JSON(t *testing.T) {
	r := &model.HealthResult{Healthy: true}
	got := FormatHealthResult(r, true)
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

// ---------- FormatGeocodeResult ----------

func TestFormatGeocodeResult_PlainText(t *testing.T) {
	got := FormatGeocodeResult(37.7749, -122.4194, "America/Los_Angeles", "San Francisco, California, USA", false)
	want := `lat: 37.7749
lon: -122.4194
timezone: America/Los_Angeles
display_name: San Francisco, California, USA`
	if got != want {
		t.Errorf("FormatGeocodeResult mismatch.\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestFormatGeocodeResult_JSON(t *testing.T) {
	got := FormatGeocodeResult(37.7749, -122.4194, "America/Los_Angeles", "San Francisco, CA", true)
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed["timezone"] != "America/Los_Angeles" {
		t.Errorf("expected timezone America/Los_Angeles, got %v", parsed["timezone"])
	}
}

// ---------- FormatTime ----------

func TestFormatTime_Pacific(t *testing.T) {
	ts := time.Date(2026, 4, 14, 1, 42, 0, 0, time.UTC) // 6:42pm PDT = 01:42 next day UTC... let's just use a known time
	got := FormatTime(ts, "America/Los_Angeles")
	want := "2026-04-13 6:42pm PDT"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatTime_UTC(t *testing.T) {
	ts := time.Date(2026, 4, 14, 13, 0, 0, 0, time.UTC)
	got := FormatTime(ts, "UTC")
	want := "2026-04-14 1:00pm UTC"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatTime_Tokyo(t *testing.T) {
	ts := time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC) // midnight UTC = 9am JST
	got := FormatTime(ts, "Asia/Tokyo")
	want := "2026-04-14 9:00am JST"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatTime_InvalidTimezone(t *testing.T) {
	ts := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
	got := FormatTime(ts, "Invalid/Zone")
	// Should fall back to the time's own location (UTC).
	want := "2026-04-14 12:00pm UTC"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ---------- FormatDuplicateError ----------

func TestFormatDuplicateError_ExactName_PlainText(t *testing.T) {
	info := &model.DuplicateInfo{
		ExistingID:   "sch_a1b2c3",
		ExistingName: "Trash bin check",
		MatchType:    "exact_name",
	}
	got := FormatDuplicateError(info, false)
	want := `Error: duplicate schedule exists
  id: sch_a1b2c3
  name: Trash bin check
  match: exact name

To create anyway:    sundial add --force ...
To update existing:  sundial remove sch_a1b2c3 && sundial add ...`
	if got != want {
		t.Errorf("FormatDuplicateError exact_name mismatch.\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestFormatDuplicateError_FuzzyName_PlainText(t *testing.T) {
	info := &model.DuplicateInfo{
		ExistingID:   "sch_x9y8z7",
		ExistingName: "Trash bin check",
		MatchType:    "fuzzy_name",
	}
	got := FormatDuplicateError(info, false)
	if !contains(got, "similar schedule exists") {
		t.Errorf("expected 'similar schedule exists' header for fuzzy match, got:\n%s", got)
	}
	if !contains(got, "similar name (close spelling)") {
		t.Errorf("expected human-readable fuzzy_name label, got:\n%s", got)
	}
	if !contains(got, "--force") {
		t.Errorf("expected --force hint, got:\n%s", got)
	}
}

func TestFormatDuplicateError_ExactCommand_PlainText(t *testing.T) {
	info := &model.DuplicateInfo{
		ExistingID:   "sch_cmd01",
		ExistingName: "My schedule",
		MatchType:    "exact_command",
	}
	got := FormatDuplicateError(info, false)
	if !contains(got, "duplicate schedule exists") {
		t.Errorf("expected 'duplicate schedule exists' header, got:\n%s", got)
	}
	if !contains(got, "exact command") {
		t.Errorf("expected 'exact command' match label, got:\n%s", got)
	}
}

func TestFormatDuplicateError_FuzzyCommand_PlainText(t *testing.T) {
	info := &model.DuplicateInfo{
		ExistingID:   "sch_cmd02",
		ExistingName: "My schedule",
		MatchType:    "fuzzy_command",
	}
	got := FormatDuplicateError(info, false)
	if !contains(got, "similar schedule exists") {
		t.Errorf("expected 'similar schedule exists' for fuzzy match, got:\n%s", got)
	}
	if !contains(got, "similar command (substring match)") {
		t.Errorf("expected human-readable fuzzy_command label, got:\n%s", got)
	}
}

func TestFormatDuplicateError_JSON(t *testing.T) {
	info := &model.DuplicateInfo{
		ExistingID:   "sch_a1b2c3",
		ExistingName: "Trash bin check",
		MatchType:    "exact_name",
	}
	got := FormatDuplicateError(info, true)
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, got)
	}
	if parsed["error"] != "duplicate schedule exists" {
		t.Errorf("expected error field, got %v", parsed["error"])
	}
	if parsed["existing_id"] != "sch_a1b2c3" {
		t.Errorf("expected existing_id, got %v", parsed["existing_id"])
	}
	if parsed["existing_name"] != "Trash bin check" {
		t.Errorf("expected existing_name, got %v", parsed["existing_name"])
	}
	if parsed["match_type"] != "exact_name" {
		t.Errorf("expected match_type, got %v", parsed["match_type"])
	}
	if parsed["hint"] == nil || parsed["hint"] == "" {
		t.Error("expected hint field")
	}
}

// ---------- FormatError ----------

func TestFormatError_PlainText(t *testing.T) {
	got := FormatError("something went wrong", false)
	want := "Error: something went wrong"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatError_JSON(t *testing.T) {
	got := FormatError("something went wrong", true)
	want := `{"error":"something went wrong"}`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --- helper ---

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
