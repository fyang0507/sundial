package model

import "time"

// ScheduleStatus represents the lifecycle state of a schedule.
type ScheduleStatus string

const (
	StatusActive    ScheduleStatus = "active"
	StatusPaused    ScheduleStatus = "paused"
	StatusCompleted ScheduleStatus = "completed"
	StatusRemoved   ScheduleStatus = "removed"
)

// RunLogType represents the type of a run log entry.
type RunLogType string

const (
	LogTypeFire        RunLogType = "fire"
	LogTypeMiss        RunLogType = "miss"
	LogTypeMissSummary RunLogType = "miss_summary"
	// LogTypeDeferred records a fire that was held back because the schedule's
	// precondition command exited non-zero. The schedule did NOT fire and
	// FireCount is unchanged; the daemon will retry after a backoff interval.
	LogTypeDeferred RunLogType = "deferred"
)

// CompletionReason records why a schedule was completed.
type CompletionReason string

const (
	CompletionTriggered CompletionReason = "triggered" // poll trigger condition matched (or --once fired)
	CompletionTimeout   CompletionReason = "timeout"   // poll timeout expired without condition match
	CompletionMissed    CompletionReason = "missed"    // at trigger fire time passed beyond grace window while daemon was offline, or a precondition never passed before the give-up budget elapsed
)

// DesiredState is the canonical schedule definition stored in the data repo.
// One JSON file per schedule at <data_repo>/sundial/schedules/sch_<id>.json.
type DesiredState struct {
	ID                string           `json:"id"`
	Name              string           `json:"name"`
	CreatedAt         time.Time        `json:"created_at"`
	UserRequest       string           `json:"user_request,omitempty"`
	Trigger           TriggerConfig    `json:"trigger"`
	Command           string           `json:"command"`
	Status            ScheduleStatus   `json:"status"`
	CompletionReason  CompletionReason `json:"completion_reason,omitempty"` // set when status=completed
	RecreationCommand string           `json:"recreation_command,omitempty"`
	Once              bool             `json:"once,omitempty"`   // fire once then complete
	Detach            bool             `json:"detach,omitempty"` // fire-and-forget: spawn without waiting for exit
	// ExecTimeout caps the command's wall-clock runtime (Go duration, e.g. "30m").
	// Empty means no timeout — the daemon waits indefinitely (today's default).
	// On expiry the command's process group is killed and the run is logged with
	// reason "timeout" and exit code -1. Only meaningful for non-detached runs:
	// a detached command's exit is never captured, so a timeout cannot apply.
	ExecTimeout string `json:"exec_timeout,omitempty"`
	// Precondition is an optional shell command run as a readiness gate before
	// EVERY fire, regardless of trigger type. Exit 0 => proceed to fire; non-zero
	// => DEFER (do not fire, do not count as a fire) and retry later with bounded
	// exponential backoff. Empty means no gate (fire unconditionally — today's
	// default). Unlike a poll trigger (which IS the trigger and checks on a fixed
	// interval), a precondition layers on top of any trigger and retries with
	// growing backoff until it passes or the give-up deadline is reached.
	Precondition string `json:"precondition,omitempty"`
	// PreconditionBackoff overrides the daemon's default backoff schedule for this
	// schedule (Go durations, e.g. ["1m","5m","30m","1h","2h"]). The Nth deferral
	// waits backoff[min(N, len-1)] — the last entry repeats as the cap. Empty =>
	// use DaemonConfig.PreconditionBackoff.
	PreconditionBackoff []string `json:"precondition_backoff,omitempty"`
	// PreconditionMaxElapsed overrides the give-up budget (Go duration). When set,
	// the daemon stops retrying once now >= first-deferred-at + max-elapsed, in
	// addition to (and taking precedence over) the default next-regular-fire bound.
	// Empty => default termination (bounded by the next regular fire for recurring
	// triggers; bounded by the daemon at-deadline budget for one-off `at`).
	PreconditionMaxElapsed string `json:"precondition_max_elapsed,omitempty"`
}

// RuntimeState is machine-local scheduling data managed by the daemon.
// Stored at ~/.config/sundial/state/sch_<id>.json.
type RuntimeState struct {
	ID           string     `json:"id"`
	NextFireAt   time.Time  `json:"next_fire_at"`
	LastFiredAt  *time.Time `json:"last_fired_at,omitempty"`
	LastExitCode *int       `json:"last_exit_code,omitempty"`
	FireCount    int        `json:"fire_count"`
	CheckCount   int        `json:"check_count,omitempty"` // poll trigger: number of condition checks run
	// PreconditionAttempts counts consecutive precondition deferrals for the
	// current pending fire. It indexes into the backoff schedule and resets to 0
	// once the precondition passes (or the daemon gives up). Zero between fires.
	PreconditionAttempts int `json:"precondition_attempts,omitempty"`
	// PreconditionFirstDeferredAt is the wall-clock time of the first deferral in
	// the current retry sequence — the anchor for the max-elapsed give-up budget.
	// Nil between fires; reset to nil when the precondition passes or we give up.
	PreconditionFirstDeferredAt *time.Time `json:"precondition_first_deferred_at,omitempty"`
	// PreconditionIntendedFire is the originally-scheduled fire time being deferred
	// (the trigger's slot, captured on the first deferral — not the backoff retry
	// instant, which mutates each attempt). Used as the give-up miss's scheduled_for
	// so the log points at the real occurrence the operator scheduled. Nil between
	// fires; reset to nil when the precondition passes or we give up.
	PreconditionIntendedFire *time.Time `json:"precondition_intended_fire,omitempty"`
}

// RunLogEntry is a single fire/miss record appended to the per-schedule JSONL log.
// Stored at ~/.config/sundial/logs/<id>.jsonl.
type RunLogEntry struct {
	Timestamp     time.Time  `json:"ts"`
	Type          RunLogType `json:"type"`
	ScheduleID    string     `json:"schedule_id"`
	ExitCode      *int       `json:"exit_code,omitempty"`
	DurationSec   *float64   `json:"duration_s,omitempty"`
	StdoutPreview string     `json:"stdout_preview,omitempty"`
	StderrPreview string     `json:"stderr_preview,omitempty"`
	Reason        string     `json:"reason,omitempty"`
	ScheduledFor  *time.Time `json:"scheduled_for,omitempty"`
	Count         int        `json:"count,omitempty"`
	From          string     `json:"from,omitempty"`
	To            string     `json:"to,omitempty"`
}
