package model

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// TriggerType distinguishes static (cron) from dynamic (solar) triggers.
type TriggerType string

const (
	TriggerTypeCron  TriggerType = "cron"
	TriggerTypeSolar TriggerType = "solar"
	TriggerTypePoll  TriggerType = "poll"
)

// SolarEvent identifies which solar event to anchor to.
type SolarEvent string

const (
	SolarEventSunrise SolarEvent = "sunrise"
	SolarEventSunset  SolarEvent = "sunset"
)

// Location holds coordinates and timezone for solar calculations.
type Location struct {
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Timezone string  `json:"timezone"`
}

// TriggerConfig is the JSON-serializable trigger definition stored in desired state.
// Exactly one of Cron or (Event+Days+Location) or (TriggerCommand+Interval) should be
// populated depending on Type.
type TriggerConfig struct {
	Type           TriggerType `json:"type"`
	Cron           string      `json:"cron,omitempty"`
	Event          SolarEvent  `json:"event,omitempty"`
	Offset         string      `json:"offset,omitempty"`          // ISO 8601 duration, e.g. "-PT1H"
	Days           []string    `json:"days,omitempty"`             // e.g. ["monday","tuesday"]
	Location       *Location   `json:"location,omitempty"`
	TriggerCommand string      `json:"trigger_command,omitempty"` // condition check command (poll)
	Interval       string      `json:"interval,omitempty"`        // Go duration, e.g. "2m" (poll)
}

// Trigger is the runtime interface for computing fire times.
// Implementations: CronTrigger, SolarTrigger (in internal/trigger/).
type Trigger interface {
	// NextFireTime returns the next fire time strictly after `after`.
	// Returns zero time if no future fire time exists.
	NextFireTime(after time.Time) time.Time

	// Validate checks the trigger configuration for errors.
	Validate() error

	// HumanDescription returns a human-readable summary.
	// e.g., "Every Mon, Tue at 1h before sunset (San Francisco)"
	HumanDescription() string
}

// ParseOffset parses human-friendly offset strings and ISO 8601 durations into time.Duration.
// Accepted formats:
//   - Human shorthand: "-1h", "+30m", "90m", "-1h30m"
//   - ISO 8601: "-PT1H", "PT30M", "-PT1H30M"
//
// Returns negative duration for "before" offsets.
func ParseOffset(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}

	// Try ISO 8601 first: optional minus, then PT, then combinations of nH, nM, nS
	if d, ok := parseISO8601Duration(s); ok {
		return d, nil
	}

	// Try Go-style / human shorthand: "-1h", "+30m", "1h30m", "-1h30m"
	d, err := time.ParseDuration(strings.TrimPrefix(s, "+"))
	if err == nil {
		return d, nil
	}

	return 0, fmt.Errorf("invalid offset %q: expected format like \"-1h\", \"+30m\", or \"-PT1H\"", s)
}

var iso8601Re = regexp.MustCompile(`^(-)?PT(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?$`)

func parseISO8601Duration(s string) (time.Duration, bool) {
	matches := iso8601Re.FindStringSubmatch(strings.ToUpper(s))
	if matches == nil {
		return 0, false
	}

	var d time.Duration
	if matches[2] != "" {
		h, _ := strconv.Atoi(matches[2])
		d += time.Duration(h) * time.Hour
	}
	if matches[3] != "" {
		m, _ := strconv.Atoi(matches[3])
		d += time.Duration(m) * time.Minute
	}
	if matches[4] != "" {
		sec, _ := strconv.Atoi(matches[4])
		d += time.Duration(sec) * time.Second
	}
	if d == 0 {
		return 0, false // "PT" alone is not valid
	}
	if matches[1] == "-" {
		d = -d
	}
	return d, true
}

// FormatOffsetISO converts a time.Duration to ISO 8601 duration string for storage.
// e.g., -1h → "-PT1H", 90m → "PT1H30M"
func FormatOffsetISO(d time.Duration) string {
	if d == 0 {
		return ""
	}

	negative := d < 0
	if negative {
		d = -d
	}

	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	var b strings.Builder
	if negative {
		b.WriteString("-")
	}
	b.WriteString("PT")
	if hours > 0 {
		fmt.Fprintf(&b, "%dH", hours)
	}
	if minutes > 0 {
		fmt.Fprintf(&b, "%dM", minutes)
	}
	if seconds > 0 {
		fmt.Fprintf(&b, "%dS", seconds)
	}
	return b.String()
}

// ParseDays converts short day names (mon,tue,...) to full lowercase names
// used in TriggerConfig.Days.
func ParseDays(input string) ([]string, error) {
	if input == "" {
		return nil, fmt.Errorf("days cannot be empty")
	}

	dayMap := map[string]string{
		"mon": "monday", "monday": "monday",
		"tue": "tuesday", "tuesday": "tuesday",
		"wed": "wednesday", "wednesday": "wednesday",
		"thu": "thursday", "thursday": "thursday",
		"fri": "friday", "friday": "friday",
		"sat": "saturday", "saturday": "saturday",
		"sun": "sunday", "sunday": "sunday",
	}

	parts := strings.Split(strings.ToLower(strings.TrimSpace(input)), ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		full, ok := dayMap[p]
		if !ok {
			return nil, fmt.Errorf("invalid day %q: expected mon,tue,wed,thu,fri,sat,sun", p)
		}
		result = append(result, full)
	}
	return result, nil
}

// DayNameToWeekday converts a full lowercase day name to time.Weekday.
func DayNameToWeekday(name string) (time.Weekday, error) {
	switch strings.ToLower(name) {
	case "sunday":
		return time.Sunday, nil
	case "monday":
		return time.Monday, nil
	case "tuesday":
		return time.Tuesday, nil
	case "wednesday":
		return time.Wednesday, nil
	case "thursday":
		return time.Thursday, nil
	case "friday":
		return time.Friday, nil
	case "saturday":
		return time.Saturday, nil
	default:
		return 0, fmt.Errorf("invalid day name %q", name)
	}
}
