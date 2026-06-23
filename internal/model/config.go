package model

// Default paths for daemon configuration.
const (
	DefaultSocketPath = "~/Library/Application Support/sundial/sundial.sock"
	DefaultLogLevel   = "info"
	DefaultLogFile    = "~/Library/Logs/sundial/sundial.log"
	DefaultStatePath  = "~/.config/sundial/state/"
	DefaultLogsPath   = "~/.config/sundial/logs/"
	// DefaultPreconditionMaxElapsed is the give-up budget for a precondition on a
	// one-off `at` trigger, which has no next regular fire to bound retries. It
	// equals the last (cap) entry of DefaultPreconditionBackoff so a single `at`
	// gives up roughly one backoff cap after its first deferral.
	DefaultPreconditionMaxElapsed = "2h"
)

// DefaultPreconditionBackoff is the daemon-wide default exponential backoff
// schedule (Go durations) for retrying a deferred precondition. The Nth deferral
// waits backoff[min(N, len-1)] — the last entry repeats as the cap. Operators
// override it globally via DaemonConfig.PreconditionBackoff, or per-schedule via
// DesiredState.PreconditionBackoff.
var DefaultPreconditionBackoff = []string{"1m", "5m", "30m", "1h", "2h"}

// Config represents the daemon configuration loaded from
// <data_repo>/sundial/config.yaml. DataRepo is injected at load time from
// the resolver (SUNDIAL_DATA_REPO / sundial.config.dev.yaml / workspace.yaml
// walk-up) — it is not a field in the on-disk schema.
type Config struct {
	DataRepo string       `yaml:"-"`
	Daemon   DaemonConfig `yaml:"daemon"`
	State    StateConfig  `yaml:"state"`
}

// DaemonConfig holds daemon-specific settings.
//
// SocketPath is intentionally not part of the on-disk schema (`yaml:"-"`): the
// CLI always dials the well-known DefaultSocketPath, so the daemon must bind it
// too — a per-repo override would let the daemon listen somewhere the CLI can
// never reach. The field is retained only so the daemon (and tests) can be
// constructed with an explicit path; production gets DefaultSocketPath via
// applyDefaults.
type DaemonConfig struct {
	SocketPath string `yaml:"-"`
	LogLevel   string `yaml:"log_level"`
	LogFile    string `yaml:"log_file"`
	// PreconditionBackoff is the default exponential backoff schedule (Go
	// durations) applied when a schedule's precondition defers a fire and the
	// schedule itself doesn't override it. Empty => DefaultPreconditionBackoff.
	PreconditionBackoff []string `yaml:"precondition_backoff"`
	// PreconditionMaxElapsed is the default give-up budget for a precondition on
	// a one-off `at` schedule (which has no next regular fire to bound retries).
	// Empty => DefaultPreconditionMaxElapsed.
	PreconditionMaxElapsed string `yaml:"precondition_max_elapsed"`
}

// StateConfig holds paths for local runtime data.
type StateConfig struct {
	Path     string `yaml:"path"`
	LogsPath string `yaml:"logs_path"`
}
