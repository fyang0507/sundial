package trigger

import (
	"fmt"
	"strings"
	"time"

	"github.com/fyang0507/sundial/internal/model"
	"github.com/robfig/cron/v3"
)

// cronParser uses the standard 5-field cron format: Minute Hour DayOfMonth Month DayOfWeek
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// CronTrigger implements model.Trigger for standard cron expressions.
type CronTrigger struct {
	Expr string
}

// NextFireTime returns the next fire time strictly after `after`.
func (t *CronTrigger) NextFireTime(after time.Time) time.Time {
	sched, err := cronParser.Parse(t.Expr)
	if err != nil {
		return time.Time{}
	}
	return sched.Next(after)
}

// Validate parses the cron expression and returns an error if it is invalid.
func (t *CronTrigger) Validate() error {
	_, err := cronParser.Parse(t.Expr)
	if err != nil {
		return fmt.Errorf("invalid cron expression %q: %w", t.Expr, err)
	}
	return nil
}

// HumanDescription returns a human-readable description of the cron schedule.
// For well-known patterns it produces natural language; otherwise it returns the raw expression.
func (t *CronTrigger) HumanDescription() string {
	desc := describeCron(t.Expr)
	if desc != "" {
		return desc
	}
	return fmt.Sprintf("cron(%s)", t.Expr)
}

// describeCron attempts to produce a human-readable description for common cron patterns.
func describeCron(expr string) string {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return ""
	}

	minute, hour, dom, month, dow := fields[0], fields[1], fields[2], fields[3], fields[4]

	// Only describe patterns where month and dom are wildcards.
	if month != "*" || dom != "*" {
		return ""
	}

	timeStr := formatCronTime(minute, hour)
	if timeStr == "" {
		return ""
	}

	dayStr := formatCronDow(dow)

	if dayStr == "" {
		return fmt.Sprintf("Every day at %s", timeStr)
	}
	return fmt.Sprintf("Every %s at %s", dayStr, timeStr)
}

// formatCronTime formats minute and hour fields into a human-readable time string.
func formatCronTime(minute, hour string) string {
	// Only handle literal minute and hour values.
	var m, h int
	if _, err := fmt.Sscanf(minute, "%d", &m); err != nil {
		return ""
	}
	if _, err := fmt.Sscanf(hour, "%d", &h); err != nil {
		return ""
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return ""
	}

	suffix := "AM"
	displayH := h
	if h == 0 {
		displayH = 12
	} else if h == 12 {
		suffix = "PM"
	} else if h > 12 {
		displayH = h - 12
		suffix = "PM"
	}

	if m == 0 {
		return fmt.Sprintf("%d:%02d %s", displayH, m, suffix)
	}
	return fmt.Sprintf("%d:%02d %s", displayH, m, suffix)
}

// formatCronDow formats the day-of-week field into a human-readable string.
func formatCronDow(dow string) string {
	if dow == "*" {
		return ""
	}

	// Handle "1-5" as weekdays
	if dow == "1-5" {
		return "weekday"
	}
	if dow == "0,6" || dow == "6,0" {
		return "Sat, Sun"
	}

	dayNames := map[string]string{
		"0": "Sun", "1": "Mon", "2": "Tue", "3": "Wed",
		"4": "Thu", "5": "Fri", "6": "Sat",
		"7": "Sun", // some cron implementations treat 7 as Sunday
	}

	parts := strings.Split(dow, ",")
	names := make([]string, 0, len(parts))
	for _, p := range parts {
		name, ok := dayNames[strings.TrimSpace(p)]
		if !ok {
			return dow
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// Compile-time assertion that CronTrigger implements model.Trigger.
var _ model.Trigger = (*CronTrigger)(nil)
