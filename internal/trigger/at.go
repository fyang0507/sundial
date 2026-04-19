package trigger

import (
	"fmt"
	"time"

	"github.com/fyang0507/sundial/internal/model"
)

// AtTrigger fires exactly once at FireAt (absolute UTC timestamp) and then
// reports "no further fires" (zero time) so the scheduler completes it.
type AtTrigger struct {
	FireAt time.Time
	// DisplayTimezone is the IANA zone used to render HumanDescription. Empty
	// means UTC. It does not affect firing — FireAt is always UTC.
	DisplayTimezone string
}

// NextFireTime returns FireAt if it is strictly after `after`, else zero.
// Returning zero is the "done forever" signal the scheduler uses to stop
// firing this schedule; combined with Once=true, it triggers completion.
func (a *AtTrigger) NextFireTime(after time.Time) time.Time {
	if a.FireAt.After(after) {
		return a.FireAt
	}
	return time.Time{}
}

// Validate requires a non-zero FireAt. Past-timestamp rejection is enforced
// at the CLI/handler layer where a clock reference is available.
func (a *AtTrigger) Validate() error {
	if a.FireAt.IsZero() {
		return fmt.Errorf("at trigger: fire_at is required")
	}
	return nil
}

// HumanDescription renders the fire time in DisplayTimezone (or UTC).
func (a *AtTrigger) HumanDescription() string {
	loc := time.UTC
	if a.DisplayTimezone != "" {
		if l, err := time.LoadLocation(a.DisplayTimezone); err == nil {
			loc = l
		}
	}
	return fmt.Sprintf("At %s", a.FireAt.In(loc).Format("Mon Jan 2 3:04 PM MST"))
}

var _ model.Trigger = (*AtTrigger)(nil)
