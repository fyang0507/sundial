package model

// Default paths for daemon configuration.
const (
	DefaultSocketPath = "~/Library/Application Support/sundial/sundial.sock"
	DefaultLogLevel   = "info"
	DefaultLogFile    = "~/Library/Logs/sundial/sundial.log"
	DefaultStatePath  = "~/.config/sundial/state/"
	DefaultLogsPath   = "~/.config/sundial/logs/"
)

// Config represents the daemon configuration loaded from config.yaml.
// The only required field is DataRepo.
type Config struct {
	DataRepo string       `yaml:"data_repo"`
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
