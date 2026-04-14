package model

import (
	"encoding/json"
	"time"
)

// RPC method names.
const (
	MethodAdd    = "add"
	MethodRemove = "remove"
	MethodList   = "list"
	MethodShow   = "show"
	MethodReload = "reload"
	MethodHealth = "health"
)

// RPCRequest is the envelope for CLI → daemon requests over the Unix socket.
type RPCRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
	ID     interface{}     `json:"id"`
}

// RPCResponse is the envelope for daemon → CLI responses.
type RPCResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
	ID     interface{}     `json:"id"`
}

// RPCError represents a structured error in an RPC response.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// --- Per-method param and result types ---

// AddParams are the parameters for the "add" RPC method.
type AddParams struct {
	Type        TriggerType `json:"type"`
	Cron        string      `json:"cron,omitempty"`
	Event       SolarEvent  `json:"event,omitempty"`
	Offset      string      `json:"offset,omitempty"`
	Days        []string    `json:"days,omitempty"`
	Lat         *float64    `json:"lat,omitempty"`
	Lon         *float64    `json:"lon,omitempty"`
	Timezone    string      `json:"timezone,omitempty"`
	Command     string      `json:"command"`
	Name        string      `json:"name,omitempty"`
	UserRequest string      `json:"user_request,omitempty"`
	Force       bool        `json:"force,omitempty"`
}

// AddResult is returned by a successful "add" RPC.
type AddResult struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Schedule    string `json:"schedule"`     // human-readable trigger description
	NextFire    string `json:"next_fire"`    // display-formatted next fire time
	NextFireUTC string `json:"next_fire_utc"` // ISO 8601 UTC for machine parsing
	Status      string `json:"status"`
	SavedTo     string `json:"saved_to"`  // data repo file path
	Committed   string `json:"committed"` // git commit message
	Recovery    string `json:"recovery,omitempty"`
	Warning     string `json:"warning,omitempty"`
}

// RemoveParams are the parameters for the "remove" RPC method.
type RemoveParams struct {
	ID  string `json:"id"`
	All bool   `json:"all,omitempty"`
}

// RemoveResult is returned by a successful "remove" RPC.
type RemoveResult struct {
	ID        string `json:"id,omitempty"`
	Removed   int    `json:"removed"` // count of schedules removed (for --all)
	Committed string `json:"committed,omitempty"`
	Warning   string `json:"warning,omitempty"`
}

// ListParams are the parameters for the "list" RPC method.
type ListParams struct{}

// ScheduleSummary is a single schedule entry in list and show results.
type ScheduleSummary struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Schedule     string     `json:"schedule"`      // human-readable trigger description
	NextFire     string     `json:"next_fire"`      // display-formatted
	NextFireUTC  string     `json:"next_fire_utc"`  // ISO 8601 UTC
	LastFire     string     `json:"last_fire,omitempty"`
	LastExitCode *int       `json:"last_exit_code,omitempty"`
	Status       string     `json:"status"`
	MissedCount  int        `json:"missed_count,omitempty"`
	MissedSince  *time.Time `json:"missed_since,omitempty"`
}

// ListResult is returned by a successful "list" RPC.
type ListResult struct {
	Schedules []ScheduleSummary `json:"schedules"`
}

// ShowParams are the parameters for the "show" RPC method.
type ShowParams struct {
	ID string `json:"id"`
}

// ShowResult is returned by a successful "show" RPC.
type ShowResult struct {
	ScheduleSummary
	Command           string `json:"command"`
	UserRequest       string `json:"user_request,omitempty"`
	CreatedAt         string `json:"created_at"`
	RecreationCommand string `json:"recreation_command,omitempty"`
}

// ReloadResult is returned by a successful "reload" RPC.
type ReloadResult struct {
	Reconciled    int    `json:"reconciled"`     // schedules reconciled
	PendingPushes bool   `json:"pending_pushes"` // whether pushes were retried
	Message       string `json:"message"`
}

// HealthCheck represents a single check in the health report.
type HealthCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "ok", "warn", "error"
	Message string `json:"message,omitempty"`
}

// HealthResult is returned by a successful "health" RPC.
type HealthResult struct {
	Healthy              bool          `json:"healthy"`
	DaemonRunning        bool          `json:"daemon_running"`
	ConfigValid          bool          `json:"config_valid"`
	DataRepoOK           bool          `json:"data_repo_ok"`
	DataRepoGitClean     bool          `json:"data_repo_git_clean"`
	ScheduleCount        int           `json:"schedule_count"`
	PendingPushes        bool          `json:"pending_pushes"`
	OrphanedSchedules    []string      `json:"orphaned_schedules,omitempty"`
	EffectivePath        string        `json:"effective_path,omitempty"`
	Checks               []HealthCheck `json:"checks"`
	ScheduleFileWarnings []string      `json:"schedule_file_warnings,omitempty"`
}

// DuplicateInfo is included in error data when a duplicate schedule is detected.
type DuplicateInfo struct {
	ExistingID   string `json:"existing_id"`
	ExistingName string `json:"existing_name"`
	MatchType    string `json:"match_type"` // "exact_name" or "exact_command"
}
