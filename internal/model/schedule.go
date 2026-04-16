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
)

// CompletionReason records why a schedule was completed.
type CompletionReason string

const (
	CompletionTriggered CompletionReason = "triggered" // poll trigger condition matched (or --once fired)
	CompletionTimeout   CompletionReason = "timeout"   // poll timeout expired without condition match
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
	Once              bool             `json:"once,omitempty"` // fire once then complete
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
