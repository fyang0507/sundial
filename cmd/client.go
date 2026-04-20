package cmd

import (
	"errors"

	"github.com/fyang0507/sundial/internal/config"
	"github.com/fyang0507/sundial/internal/ipc"
	"github.com/fyang0507/sundial/internal/model"
)

// getClient resolves the data repo, loads the daemon config to learn the
// socket path, and returns an IPC client. If the data repo cannot be
// resolved, it falls back to the default socket path so that CLI commands
// can still probe the daemon (e.g. `sundial health`).
func getClient() (*ipc.Client, error) {
	cfg, _, err := config.LoadAndResolve()
	if err != nil {
		if errors.Is(err, model.ErrDataRepoNotResolved) {
			return ipc.NewClient(config.ExpandPath(model.DefaultSocketPath)), nil
		}
		return nil, err
	}
	return ipc.NewClient(cfg.Daemon.SocketPath), nil
}
