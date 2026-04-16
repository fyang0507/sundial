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
type PollTrigger struct {
	TriggerCommand string
	Interval       time.Duration
}

// NextFireTime returns after + Interval.
func (p *PollTrigger) NextFireTime(after time.Time) time.Time {
	return after.Add(p.Interval)
}

// Validate checks that the trigger command is non-empty and the interval is
// at least minPollInterval.
func (p *PollTrigger) Validate() error {
	if p.TriggerCommand == "" {
		return fmt.Errorf("poll trigger: trigger_command is required")
	}
	if p.Interval < minPollInterval {
		return fmt.Errorf("poll trigger: interval must be at least %s, got %s", minPollInterval, p.Interval)
	}
	return nil
}

// HumanDescription returns a human-readable summary of the poll trigger.
func (p *PollTrigger) HumanDescription() string {
	return fmt.Sprintf("Poll every %s", p.Interval)
}

// Compile-time assertion that PollTrigger implements model.Trigger.
var _ model.Trigger = (*PollTrigger)(nil)
