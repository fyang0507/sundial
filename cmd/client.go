package cmd

import (
	"github.com/fyang0507/sundial/internal/config"
	"github.com/fyang0507/sundial/internal/ipc"
)

// getClient loads the config and returns an IPC client connected to the daemon.
func getClient() (*ipc.Client, error) {
	cfgPath, err := config.FindConfigPath()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	return ipc.NewClient(cfg.Daemon.SocketPath), nil
}
