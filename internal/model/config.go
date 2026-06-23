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

	// DefaultWakeLeadTime is how far before a due fire the daemon schedules a
	// pmset wake event when wake management is enabled. A few minutes of lead
	// gives the machine time to fully resume (and the watchdog loop time to tick)
	// before the fire instant, so the schedule fires on time rather than racing
	// the wake. Override via DaemonConfig.Wake.LeadTime.
	DefaultWakeLeadTime = "3m"

	// DefaultMissGracePeriod is the window within which a fire missed while the
	// daemon was offline or asleep is still executed once on the next tick rather
	// than logged as a miss. 60s comfortably covers the brief gaps of a daemon
	// restart or a short sleep while still keeping a stale catch-up fire from
	// running long after its scheduled instant. Override via
	// DaemonConfig.MissGracePeriod.
	DefaultMissGracePeriod = "60s"
)

// DefaultPreconditionBackoff is the daemon-wide default exponential backoff
// schedule (Go durations) for retrying a deferred precondition. The Nth deferral
// waits backoff[min(N, len-1)] — the last entry repeats as the cap. Operators
// override it globally via DaemonConfig.PreconditionBackoff, or per-schedule via
// DesiredState.PreconditionBackoff.
var DefaultPreconditionBackoff = []string{"1m", "5m", "30m", "1h", "2h"}

// Config represents the entire sundial configuration loaded from a single
// config file (sundial.config.yaml) that lives in the sundial source repo.
// The file holds everything: the top-level data_repo_path plus the daemon and
// state option blocks. The daemon reads it at startup via the --config flag.
//
// DataRepo is the in-memory data-repo path; it is populated from the on-disk
// data_repo_path field (and may be overridden by --data-repo / SUNDIAL_DATA_REPO).
// ConfigPath is the absolute path of the config file the daemon was loaded
// from; it is not part of the on-disk schema (yaml:"-") — it is set by the
// loader so the daemon can report it in `sundial health`.
type Config struct {
	DataRepo   string       `yaml:"data_repo_path"`
	ConfigPath string       `yaml:"-"`
	Daemon     DaemonConfig `yaml:"daemon"`
	State      StateConfig  `yaml:"state"`
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
	// MissGracePeriod is the window (Go duration string, e.g. "60s") within which
	// a fire missed while the daemon was offline or asleep is still executed once
	// on the next tick. A miss inside the window is left in the past so the run
	// loop fires it exactly once (coalescing repeated missed occurrences into a
	// single catch-up); a miss beyond the window is logged as a miss and the
	// schedule advanced to its next future fire. Empty => DefaultMissGracePeriod.
	MissGracePeriod string `yaml:"miss_grace_period"`
	// Wake configures the optional macOS pmset wake integration: when enabled the
	// daemon manages a single pmset wake event for the soonest fire across all
	// active schedules so a sleeping Mac wakes before a schedule is due.
	Wake WakeConfig `yaml:"wake"`
}

// WakeConfig is the global, opt-in toggle for macOS pmset wake management.
//
// It is intentionally daemon-global rather than per-schedule: the daemon owns
// exactly ONE pmset wake event (for the soonest fire across all active
// schedules), so the policy "should the machine ever wake itself for sundial"
// is a single system-wide decision, not something each schedule re-litigates.
//
// Enabling requires a NOPASSWD sudoers rule (pmset needs root); `sundial health`
// and setup.md print the exact line. When the rule is absent or pmset is
// unavailable, the daemon disables wake management gracefully and never breaks
// scheduling.
type WakeConfig struct {
	// Enabled turns pmset wake management on. Default false: sundial never
	// touches the machine's power schedule unless the operator opts in.
	Enabled bool `yaml:"enabled"`
	// LeadTime is how far before the soonest fire to wake (Go duration). Empty =>
	// DefaultWakeLeadTime.
	LeadTime string `yaml:"lead_time"`
}

// StateConfig holds paths for local runtime data.
type StateConfig struct {
	Path     string `yaml:"path"`
	LogsPath string `yaml:"logs_path"`
}
