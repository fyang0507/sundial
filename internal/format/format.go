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
	// Remind the operator about the daemon-wide active-hours window: an obeying
	// schedule only fires inside it (its next_fire is already clamped), and a
	// truly time-critical job can opt out. A schedule that already opted out gets
	// a confirmation instead.
	if r.IgnoreActiveHours {
		kv(&b, "active_hours", "ignored (this schedule fires at any hour)")
	} else if r.ActiveHours != "" {
		kv(&b, "active_hours", fmt.Sprintf("%s — this schedule fires only in this window (next_fire is clamped to it); pass --ignore-active-hours to override", r.ActiveHours))
	}
	if r.Recovery != "" {
		kv(&b, "recovery", r.Recovery)
	}
	if r.Warning != "" {
		kv(&b, "warning", r.Warning)
	}
	return strings.TrimRight(b.String(), "\n")
}

// FormatSetActiveHoursResult formats a SetActiveHoursResult for display.
func FormatSetActiveHoursResult(r *model.SetActiveHoursResult, jsonMode bool) string {
	if jsonMode {
		return mustMarshal(r)
	}
	var b strings.Builder
	if r.Cleared {
		kv(&b, "active_hours", "cleared (schedules fire at any hour)")
	} else {
		kv(&b, "active_hours", r.ActiveHours)
	}
	kv(&b, "reclamped", fmt.Sprintf("%d schedule(s) re-clamped to the new window", r.Reclamped))
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
		status := s.Status
		// Append a compact tag for a schedule currently held in precondition
		// backoff, so list flags it at a glance without adding a column. The
		// NEXT FIRE shown is the retry instant, not the natural slot.
		if s.Deferred {
			status += " [deferred]"
		}
		// Flag a schedule currently held back by its active-hours window; NEXT
		// FIRE is then the window opening rather than the natural slot.
		if s.Suppressed {
			status += " [outside active hours]"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.ID, s.Name, schedule, s.NextFire, status)
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
		} else if r.Detach {
			lastLine = fmt.Sprintf("%s (detached)", r.LastFire)
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

	// Schedule configuration: only emit lines for options the operator actually
	// set, so a plain schedule stays terse. Backoff/max_elapsed are per-schedule
	// overrides — surface them only when present (otherwise the daemon default
	// applies, which is not schedule-specific).
	if r.ExecTimeout != "" {
		kv(&b, "exec_timeout", r.ExecTimeout)
	}
	if r.Precondition != "" {
		kv(&b, "precondition", r.Precondition)
		if len(r.PreconditionBackoff) > 0 {
			kv(&b, "precondition_backoff", strings.Join(r.PreconditionBackoff, ", "))
		}
		if r.PreconditionMaxElapsed != "" {
			kv(&b, "precondition_max_elapsed", r.PreconditionMaxElapsed)
		}
	}

	// Live precondition-backoff state: a clear line so an operator can tell the
	// schedule is waiting on a precondition (NextFire is the retry instant) rather
	// than genuinely idle. Render the retry time in LOCAL time to match the
	// wake/next-fire convention.
	if r.PreconditionDeferred {
		retry := localTime(r.PreconditionRetryAt)
		kv(&b, "deferred", fmt.Sprintf("precondition not met (attempt %d, next retry %s)", r.PreconditionAttempts, retry))
	}

	// Active-hours: whether the schedule obeys the daemon-wide window (and is
	// presently suppressed inside a closed period) or is exempt.
	if r.IgnoreActiveHours {
		kv(&b, "active_hours", "ignored (fires at any hour)")
	} else if r.ActiveHours != "" {
		line := r.ActiveHours
		if r.Suppressed {
			line += " (currently outside window — next fire deferred to window opening)"
		}
		kv(&b, "active_hours", line)
	}

	return strings.TrimRight(b.String(), "\n")
}

// FormatHealthResult formats a HealthResult as a section-based report.
func FormatHealthResult(r *model.HealthResult, jsonMode bool) string {
	if jsonMode {
		return mustMarshal(r)
	}

	var b strings.Builder
	b.WriteString("sundial health\n\n")
	launchdStatus := "no"
	if r.Launchd {
		launchdStatus = "yes"
	}
	kv(&b, "daemon", "ok")
	kv(&b, "pid", fmt.Sprintf("%d", r.PID))
	kv(&b, "uptime", r.Uptime)
	kv(&b, "data_repo", r.DataRepo)
	if r.Config != "" {
		kv(&b, "config", r.Config)
	}
	kv(&b, "socket_path", r.SocketPath)
	kv(&b, "log_level", r.LogLevel)
	kv(&b, "log_file", r.LogFile)
	kv(&b, "launchd", launchdStatus)
	kv(&b, "schedules", fmt.Sprintf("%d active", r.ScheduleCount))
	activeHoursStatus := "disabled (schedules fire at any hour)"
	if r.ActiveHours != "" {
		activeHoursStatus = r.ActiveHours + " (schedules fire only in this window unless --ignore-active-hours)"
	}
	kv(&b, "active_hours", activeHoursStatus)

	// Wake section: only meaningful detail when the operator opted in. When
	// disabled we keep it to a single line so health stays scannable.
	if r.WakeEnabled {
		wakeStatus := "enabled"
		switch {
		case !r.PmsetAvailable:
			wakeStatus = "enabled (inactive: pmset unavailable)"
		case !r.WakePermission:
			wakeStatus = "enabled (inactive: sudoers not configured)"
		case r.WakeNextAt != "":
			// WakeNextAt is RFC3339 UTC (for machine consumers of --json). Render it
			// in LOCAL time here so it matches what `pmset -g sched` prints (pmset
			// lists events in local wall-clock time), avoiding a confusing UTC-vs-local
			// discrepancy when the operator cross-checks.
			wakeStatus = "enabled (next wake " + localWakeTime(r.WakeNextAt) + ")"
		default:
			wakeStatus = "enabled (no upcoming fire)"
		}
		kv(&b, "wake", wakeStatus)
		// Mirror how git recovery commands are surfaced: an actionable line the
		// operator can copy-paste to fix the missing permission.
		if r.WakeSudoersHint != "" {
			kv(&b, "  fix", "add to sudoers (run 'sudo visudo'): "+r.WakeSudoersHint)
		}
	} else {
		kv(&b, "wake", "disabled")
	}
	return strings.TrimRight(b.String(), "\n")
}

// localTime renders an RFC3339 UTC timestamp in the host's local time. On a
// parse error it returns the input verbatim so output never hides the raw value.
// Shared by the wake (pmset) and precondition-deferral renderers so both present
// machine timestamps in consistent local wall-clock time.
func localTime(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	return t.Local().Format("2006-01-02 15:04:05 MST")
}

// localWakeTime renders an RFC3339 UTC timestamp in the host's local time, to
// match `pmset -g sched` output (pmset lists events in local wall-clock time).
func localWakeTime(rfc3339 string) string {
	return localTime(rfc3339)
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

// FormatNotFoundError formats a not-found error with available IDs and hints.
func FormatNotFoundError(info *model.NotFoundInfo, jsonMode bool) string {
	if jsonMode {
		m := map[string]interface{}{
			"error":       "schedule not found",
			"searched_id": info.SearchedID,
			"hint":        info.Hint,
		}
		if len(info.AvailableIDs) > 0 {
			m["available_ids"] = info.AvailableIDs
		}
		if info.ClosestMatch != "" {
			m["closest_match"] = info.ClosestMatch
		}
		return mustMarshal(m)
	}

	var b strings.Builder
	b.WriteString("Error: schedule not found\n")
	kv(&b, "  searched", info.SearchedID)
	if info.ClosestMatch != "" {
		kv(&b, "  closest", info.ClosestMatch)
	}
	kv(&b, "  hint", info.Hint)
	return strings.TrimRight(b.String(), "\n")
}

// FormatGitPreconditionError formats a git precondition failure with recovery commands.
func FormatGitPreconditionError(info *model.GitPreconditionInfo, jsonMode bool) string {
	if jsonMode {
		m := map[string]interface{}{
			"error":             "git precondition failed",
			"failure_type":      info.FailureType,
			"data_repo_path":    info.DataRepoPath,
			"recovery_commands": info.RecoveryCommands,
		}
		return mustMarshal(m)
	}

	var b strings.Builder
	b.WriteString("Error: git precondition failed\n")
	kv(&b, "  type", info.FailureType)
	kv(&b, "  repo", info.DataRepoPath)
	for _, cmd := range info.RecoveryCommands {
		kv(&b, "  recover", cmd)
	}
	return strings.TrimRight(b.String(), "\n")
}

// FormatStateConflictError formats a state conflict error with the current status
// and a suggested command to resolve it.
func FormatStateConflictError(info *model.StateConflictInfo, jsonMode bool) string {
	if jsonMode {
		m := map[string]interface{}{
			"error":             fmt.Sprintf("schedule is already %s", info.CurrentStatus),
			"schedule_id":       info.ScheduleID,
			"schedule_name":     info.ScheduleName,
			"current_status":    info.CurrentStatus,
			"suggested_command": info.SuggestedCommand,
		}
		return mustMarshal(m)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Error: schedule is already %s\n", info.CurrentStatus)
	kv(&b, "  id", info.ScheduleID)
	kv(&b, "  name", info.ScheduleName)
	kv(&b, "  status", info.CurrentStatus)
	kv(&b, "  hint", fmt.Sprintf("run %q to change state", info.SuggestedCommand))
	return strings.TrimRight(b.String(), "\n")
}

// FormatDaemonUnreachableError formats a daemon-unreachable error with socket
// details and startup hints.
func FormatDaemonUnreachableError(info *model.DaemonUnreachableError, jsonMode bool) string {
	reason := info.FailureReason
	if reason == "socket_not_found" {
		reason = "socket not found"
	} else if reason == "connection_refused" {
		reason = "connection refused"
	}

	if jsonMode {
		m := map[string]interface{}{
			"error":       "daemon is not running",
			"socket_path": info.SocketPath,
			"reason":      info.FailureReason,
			"hint":        `run "sundial install" to set up the daemon, or "sundial daemon" to start manually`,
		}
		return mustMarshal(m)
	}

	var b strings.Builder
	b.WriteString("Error: daemon is not running\n")
	kv(&b, "  socket", fmt.Sprintf("%s (%s)", info.SocketPath, reason))
	kv(&b, "  hint", `run "sundial install" to set up the daemon, or "sundial daemon" to start manually`)
	return strings.TrimRight(b.String(), "\n")
}

// FormatInvalidTriggerError formats an invalid-trigger error with the trigger type
// and a corrective example.
func FormatInvalidTriggerError(info *model.InvalidTriggerInfo, jsonMode bool) string {
	if jsonMode {
		m := map[string]interface{}{
			"error":        "invalid trigger",
			"trigger_type": info.TriggerType,
			"raw_error":    info.RawError,
			"example":      info.Example,
		}
		return mustMarshal(m)
	}

	var b strings.Builder
	b.WriteString("Error: invalid trigger\n")
	kv(&b, "  type", info.TriggerType)
	kv(&b, "  detail", info.RawError)
	if info.Example != "" {
		kv(&b, "  example", info.Example)
	}
	return strings.TrimRight(b.String(), "\n")
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
