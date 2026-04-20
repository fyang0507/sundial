package model

// Default paths for daemon configuration.
const (
	DefaultSocketPath = "~/Library/Application Support/sundial/sundial.sock"
	DefaultLogLevel   = "info"
	DefaultLogFile    = "~/Library/Logs/sundial/sundial.log"
	DefaultStatePath  = "~/.config/sundial/state/"
	DefaultLogsPath   = "~/.config/sundial/logs/"
)

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
type DaemonConfig struct {
	SocketPath string `yaml:"socket_path"`
	LogLevel   string `yaml:"log_level"`
	LogFile    string `yaml:"log_file"`
}

// StateConfig holds paths for local runtime data.
type StateConfig struct {
	Path     string `yaml:"path"`
	LogsPath string `yaml:"logs_path"`
}
