package trigger

import (
	"fmt"
	"time"

	"github.com/fyang0507/sundial/internal/model"
)

// minPollInterval is the minimum allowed polling interval.
const minPollInterval = 30 * time.Second

// PollTrigger implements model.Trigger for condition-gated periodic execution.
// The daemon periodically runs TriggerCommand; the main command fires only
// when the check exits 0. NextFireTime simply adds Interval to the reference time.
// Timeout sets the maximum lifetime of the schedule: once created_at + Timeout
// has elapsed, the daemon fires the main command one final time and completes
// the schedule, regardless of the trigger check result.
type PollTrigger struct {
	TriggerCommand string
	Interval       time.Duration
	Timeout        time.Duration
}

// NextFireTime returns after + Interval.
func (p *PollTrigger) NextFireTime(after time.Time) time.Time {
	return after.Add(p.Interval)
}

// Validate checks that the trigger command is non-empty, the interval is
// at least minPollInterval, timeout is positive, and timeout >= interval.
func (p *PollTrigger) Validate() error {
	if p.TriggerCommand == "" {
		return fmt.Errorf("poll trigger: trigger_command is required")
	}
	if p.Interval < minPollInterval {
		return fmt.Errorf("poll trigger: interval must be at least %s, got %s", minPollInterval, p.Interval)
	}
	if p.Timeout <= 0 {
		return fmt.Errorf("poll trigger: timeout is required and must be positive")
	}
	if p.Timeout < p.Interval {
		return fmt.Errorf("poll trigger: timeout (%s) must be at least as long as interval (%s)", p.Timeout, p.Interval)
	}
	return nil
}

// HumanDescription returns a human-readable summary of the poll trigger.
func (p *PollTrigger) HumanDescription() string {
	return fmt.Sprintf("Poll every %s (timeout %s)", p.Interval, p.Timeout)
}

// Compile-time assertion that PollTrigger implements model.Trigger.
var _ model.Trigger = (*PollTrigger)(nil)
