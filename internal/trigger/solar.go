package trigger

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/fyang0507/sundial/internal/model"
	"github.com/nathan-osman/go-sunrise"
)

// SolarTrigger implements model.Trigger for sunrise/sunset-based schedules.
type SolarTrigger struct {
	Event    model.SolarEvent
	Offset   time.Duration
	Days     []time.Weekday
	Location model.Location
}

// NextFireTime returns the next fire time strictly after `after`.
// It scans up to 366 days ahead to find a matching day and computes the solar event time.
func (t *SolarTrigger) NextFireTime(after time.Time) time.Time {
	loc, err := time.LoadLocation(t.Location.Timezone)
	if err != nil {
		return time.Time{}
	}

	// Convert `after` to the local timezone so we iterate by local calendar days.
	localAfter := after.In(loc)

	for i := 0; i <= 366; i++ {
		candidate := localAfter.AddDate(0, 0, i)
		candidateDay := candidate.Weekday()

		if !t.matchesDay(candidateDay) {
			continue
		}

		// Compute solar event for this calendar date.
		year, month, day := candidate.Date()
		rise, set := sunrise.SunriseSunset(
			t.Location.Lat, t.Location.Lon,
			year, month, day,
		)

		var eventTime time.Time
		switch t.Event {
		case model.SolarEventSunrise:
			eventTime = rise
		case model.SolarEventSunset:
			eventTime = set
		default:
			return time.Time{}
		}

		fireTime := eventTime.Add(t.Offset)

		if fireTime.After(after) {
			return fireTime
		}
	}

	return time.Time{}
}

// Validate checks that the solar trigger configuration is valid.
func (t *SolarTrigger) Validate() error {
	if t.Location.Lat < -90 || t.Location.Lat > 90 {
		return fmt.Errorf("latitude %f out of range [-90, 90]", t.Location.Lat)
	}
	if t.Location.Lon < -180 || t.Location.Lon > 180 {
		return fmt.Errorf("longitude %f out of range [-180, 180]", t.Location.Lon)
	}
	if _, err := time.LoadLocation(t.Location.Timezone); err != nil {
		return fmt.Errorf("invalid timezone %q: %w", t.Location.Timezone, err)
	}
	if t.Event != model.SolarEventSunrise && t.Event != model.SolarEventSunset {
		return fmt.Errorf("invalid solar event %q: expected %q or %q", t.Event, model.SolarEventSunrise, model.SolarEventSunset)
	}
	if len(t.Days) == 0 {
		return fmt.Errorf("days list must not be empty")
	}
	return nil
}

// HumanDescription returns a human-readable summary of the solar trigger.
func (t *SolarTrigger) HumanDescription() string {
	dayNames := make([]string, len(t.Days))
	for i, d := range t.Days {
		dayNames[i] = d.String()[:3]
	}

	offsetStr := ""
	if t.Offset != 0 {
		abs := t.Offset
		if abs < 0 {
			abs = -abs
		}
		offsetStr = formatDuration(abs)
		if t.Offset < 0 {
			offsetStr += " before"
		} else {
			offsetStr += " after"
		}
		offsetStr += " "
	} else {
		offsetStr = "at "
	}

	return fmt.Sprintf("Every %s %s%s (%.4f, %.4f)",
		strings.Join(dayNames, ", "),
		offsetStr,
		string(t.Event),
		t.Location.Lat,
		t.Location.Lon,
	)
}

// matchesDay checks whether the given weekday is in the trigger's Days list.
func (t *SolarTrigger) matchesDay(day time.Weekday) bool {
	for _, d := range t.Days {
		if d == day {
			return true
		}
	}
	return false
}

// formatDuration formats a duration into a human-readable string like "1h30m".
func formatDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}

	hours := int(math.Floor(d.Hours()))
	minutes := int(d.Minutes()) - hours*60

	var parts []string
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if len(parts) == 0 {
		seconds := int(d.Seconds())
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}
	return strings.Join(parts, "")
}

// Compile-time assertion that SolarTrigger implements model.Trigger.
var _ model.Trigger = (*SolarTrigger)(nil)
