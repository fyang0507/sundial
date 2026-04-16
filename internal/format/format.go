package format

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/fyang0507/sundial/internal/model"
)

// FormatAddResult formats an AddResult for display. When jsonMode is true the
// result is returned as compact JSON; otherwise it is rendered as aligned
// key:value lines matching the design doc.
func FormatAddResult(r *model.AddResult, jsonMode bool) string {
	if jsonMode {
		return mustMarshal(r)
	}

	var b strings.Builder
	kv(&b, "id", r.ID)
	kv(&b, "name", r.Name)
	kv(&b, "schedule", r.Schedule)
	kv(&b, "next_fire", r.NextFire)
	kv(&b, "status", r.Status)
	kv(&b, "saved_to", r.SavedTo)
	kv(&b, "committed", r.Committed)
	if r.Recovery != "" {
		kv(&b, "recovery", r.Recovery)
	}
	if r.Warning != "" {
		kv(&b, "warning", r.Warning)
	}
	return strings.TrimRight(b.String(), "\n")
}

// FormatRemoveResult formats a RemoveResult for display.
func FormatRemoveResult(r *model.RemoveResult, jsonMode bool) string {
	if jsonMode {
		return mustMarshal(r)
	}
	var b strings.Builder
	if r.Removed > 1 {
		kv(&b, "removed", fmt.Sprintf("%d schedules", r.Removed))
	} else {
		kv(&b, "removed", r.ID)
	}
	if r.Warning != "" {
		kv(&b, "warning", r.Warning)
	}
	return strings.TrimRight(b.String(), "\n")
}

// FormatPauseResult formats a PauseResult for display.
func FormatPauseResult(r *model.PauseResult, jsonMode bool) string {
	if jsonMode {
		return mustMarshal(r)
	}
	var b strings.Builder
	kv(&b, "id", r.ID)
	kv(&b, "name", r.Name)
	kv(&b, "status", r.Status)
	if r.NextFire != "" {
		kv(&b, "next_fire", r.NextFire)
	}
	if r.Warning != "" {
		kv(&b, "warning", r.Warning)
	}
	return strings.TrimRight(b.String(), "\n")
}

// FormatListResult formats a ListResult as a tabular table for plain text or
// compact JSON. An empty schedule list produces "No schedules found."
func FormatListResult(r *model.ListResult, jsonMode bool) string {
	if jsonMode {
		return mustMarshal(r)
	}
	if len(r.Schedules) == 0 {
		return "No schedules found."
	}

	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 4, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSCHEDULE\tNEXT FIRE\tSTATUS")
	for _, s := range r.Schedules {
		schedule := truncate(s.Schedule, 30)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.ID, s.Name, schedule, s.NextFire, s.Status)
	}
	tw.Flush()
	return strings.TrimRight(buf.String(), "\n")
}

// FormatShowResult formats a ShowResult as key:value pairs.
func FormatShowResult(r *model.ShowResult, jsonMode bool) string {
	if jsonMode {
		return mustMarshal(r)
	}

	var b strings.Builder
	kv(&b, "id", r.ID)
	kv(&b, "name", r.Name)
	kv(&b, "schedule", r.Schedule)
	kv(&b, "next_fire", r.NextFire)

	if r.LastFire != "" {
		lastLine := r.LastFire
		if r.LastExitCode != nil {
			lastLine = fmt.Sprintf("%s (exit %d)", r.LastFire, *r.LastExitCode)
		}
		kv(&b, "last_fire", lastLine)
	}

	if r.MissedCount > 0 {
		missedLine := fmt.Sprintf("%d since last fire", r.MissedCount)
		if r.MissedSince != nil {
			missedLine += fmt.Sprintf(" (daemon offline since %s)", r.MissedSince.Format("2006-01-02"))
		}
		kv(&b, "missed", missedLine)
	}

	kv(&b, "status", r.Status)
	kv(&b, "command", r.Command)
	return strings.TrimRight(b.String(), "\n")
}

// FormatHealthResult formats a HealthResult as a section-based report.
func FormatHealthResult(r *model.HealthResult, jsonMode bool) string {
	if jsonMode {
		return mustMarshal(r)
	}

	var b strings.Builder
	b.WriteString("sundial health\n\n")

	for _, c := range r.Checks {
		line := c.Status
		if c.Message != "" {
			line += " (" + c.Message + ")"
		}
		kv(&b, c.Name, line)
	}

	kv(&b, "schedules", fmt.Sprintf("%d active", r.ScheduleCount))
	if r.EffectivePath != "" {
		kv(&b, "path", r.EffectivePath)
	}

	// Collect warnings from orphaned schedules and schedule file warnings.
	var warnings []string
	if len(r.OrphanedSchedules) > 0 {
		warnings = append(warnings, fmt.Sprintf("%d orphaned schedule: %s",
			len(r.OrphanedSchedules), strings.Join(r.OrphanedSchedules, ", ")))
	}
	warnings = append(warnings, r.ScheduleFileWarnings...)

	if len(warnings) > 0 {
		b.WriteString("\nwarnings:\n")
		for _, w := range warnings {
			b.WriteString("  - " + w + "\n")
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

// FormatGeocodeResult formats geocode output as key:value pairs or JSON.
func FormatGeocodeResult(lat, lon float64, tz, display string, jsonMode bool) string {
	if jsonMode {
		m := map[string]interface{}{
			"lat":          lat,
			"lon":          lon,
			"timezone":     tz,
			"display_name": display,
		}
		return mustMarshal(m)
	}
	var b strings.Builder
	kv(&b, "lat", fmt.Sprintf("%.4f", lat))
	kv(&b, "lon", fmt.Sprintf("%.4f", lon))
	kv(&b, "timezone", tz)
	kv(&b, "display_name", display)
	return strings.TrimRight(b.String(), "\n")
}

// FormatTime formats a time.Time for display, converted to the given IANA
// timezone. The output format is "2006-01-02 3:04pm MST".
func FormatTime(t time.Time, tz string) string {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		// Fall back to the time's own location.
		loc = t.Location()
	}
	return t.In(loc).Format("2006-01-02 3:04pm MST")
}

// FormatDuplicateError formats a duplicate-schedule error with the structured
// DuplicateInfo data and actionable hints for both agents and humans.
func FormatDuplicateError(info *model.DuplicateInfo, jsonMode bool) string {
	matchLabel := humanMatchType(info.MatchType)
	isFuzzy := strings.HasPrefix(info.MatchType, "fuzzy_")

	if jsonMode {
		m := map[string]interface{}{
			"error":         "duplicate schedule exists",
			"existing_id":   info.ExistingID,
			"existing_name": info.ExistingName,
			"match_type":    info.MatchType,
			"hint":          "use --force to override, or sundial remove " + info.ExistingID + " first",
		}
		return mustMarshal(m)
	}

	var b strings.Builder
	if isFuzzy {
		b.WriteString("Error: similar schedule exists\n")
	} else {
		b.WriteString("Error: duplicate schedule exists\n")
	}
	kv(&b, "  id", info.ExistingID)
	kv(&b, "  name", info.ExistingName)
	kv(&b, "  match", matchLabel)
	b.WriteByte('\n')
	b.WriteString("To create anyway:    sundial add --force ...\n")
	b.WriteString("To update existing:  sundial remove " + info.ExistingID + " && sundial add ...\n")
	return strings.TrimRight(b.String(), "\n")
}

// humanMatchType converts a DuplicateInfo.MatchType to a human-readable label.
func humanMatchType(mt string) string {
	switch mt {
	case "exact_name":
		return "exact name"
	case "exact_command":
		return "exact command"
	case "fuzzy_name":
		return "similar name (close spelling)"
	case "fuzzy_command":
		return "similar command (substring match)"
	default:
		return mt
	}
}

// FormatError formats an error message for display.
func FormatError(msg string, jsonMode bool) string {
	if jsonMode {
		m := map[string]string{"error": msg}
		return mustMarshal(m)
	}
	return "Error: " + msg
}

// --- helpers ---

// kv writes a "key: value\n" line.
func kv(b *strings.Builder, key, value string) {
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteByte('\n')
}

// truncate shortens s to maxLen characters, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// mustMarshal marshals v to compact JSON, panicking on error (should never
// happen for well-formed result types).
func mustMarshal(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("format: json.Marshal: %v", err))
	}
	return string(data)
}
