package trigger

import (
	"fmt"
	"time"

	"github.com/fyang0507/sundial/internal/model"
)

// ParseTrigger creates a model.Trigger from a TriggerConfig.
// It dispatches on cfg.Type and returns an error for unknown types.
func ParseTrigger(cfg model.TriggerConfig) (model.Trigger, error) {
	switch cfg.Type {
	case model.TriggerTypeCron:
		return parseCronTrigger(cfg)
	case model.TriggerTypeSolar:
		return parseSolarTrigger(cfg)
	case model.TriggerTypePoll:
		return parsePollTrigger(cfg)
	case model.TriggerTypeAt:
		return parseAtTrigger(cfg)
	default:
		return nil, fmt.Errorf("unknown trigger type %q", cfg.Type)
	}
}

func parseAtTrigger(cfg model.TriggerConfig) (*AtTrigger, error) {
	if cfg.FireAt == "" {
		return nil, fmt.Errorf("at trigger: fire_at is required")
	}
	fireAt, err := time.Parse(time.RFC3339, cfg.FireAt)
	if err != nil {
		return nil, fmt.Errorf("invalid fire_at %q: %w", cfg.FireAt, err)
	}
	t := &AtTrigger{
		FireAt: fireAt.UTC(),
	}
	if cfg.Location != nil {
		t.DisplayTimezone = cfg.Location.Timezone
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	return t, nil
}

func parseCronTrigger(cfg model.TriggerConfig) (*CronTrigger, error) {
	t := &CronTrigger{
		Expr: cfg.Cron,
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	return t, nil
}

func parsePollTrigger(cfg model.TriggerConfig) (*PollTrigger, error) {
	interval, err := time.ParseDuration(cfg.Interval)
	if err != nil {
		return nil, fmt.Errorf("invalid interval %q: %w", cfg.Interval, err)
	}

	timeout, err := time.ParseDuration(cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("invalid timeout %q: %w", cfg.Timeout, err)
	}

	t := &PollTrigger{
		TriggerCommand: cfg.TriggerCommand,
		Interval:       interval,
		Timeout:        timeout,
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	return t, nil
}

func parseSolarTrigger(cfg model.TriggerConfig) (*SolarTrigger, error) {
	offset, err := model.ParseOffset(cfg.Offset)
	if err != nil {
		return nil, fmt.Errorf("invalid offset: %w", err)
	}

	days := make([]time.Weekday, 0, len(cfg.Days))
	for _, name := range cfg.Days {
		wd, err := model.DayNameToWeekday(name)
		if err != nil {
			return nil, fmt.Errorf("invalid day: %w", err)
		}
		days = append(days, wd)
	}

	loc := model.Location{}
	if cfg.Location != nil {
		loc = *cfg.Location
	}

	t := &SolarTrigger{
		Event:    cfg.Event,
		Offset:   offset,
		Days:     days,
		Location: loc,
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	return t, nil
}
