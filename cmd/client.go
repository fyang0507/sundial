package cmd

import (
	"errors"

	"github.com/fyang0507/sundial/internal/config"
	"github.com/fyang0507/sundial/internal/ipc"
	"github.com/fyang0507/sundial/internal/model"
)

// getClient loads the config and returns an IPC client connected to the daemon.
// If no config file is found, it falls back to the default socket path so that
// CLI commands work without requiring SUNDIAL_CONFIG or a config file.
func getClient() (*ipc.Client, error) {
	cfgPath, err := config.FindConfigPath()
	if err != nil {
		if errors.Is(err, model.ErrConfigNotFound) {
			socketPath := config.ExpandPath(model.DefaultSocketPath)
			return ipc.NewClient(socketPath), nil
		}
		return nil, err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	return ipc.NewClient(cfg.Daemon.SocketPath), nil
}
