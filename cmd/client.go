package cmd

import (
	"github.com/fyang0507/sundial/internal/config"
	"github.com/fyang0507/sundial/internal/ipc"
	"github.com/fyang0507/sundial/internal/model"
)

// getClient returns an IPC client dialing the well-known daemon socket. The
// CLI is directory-agnostic: it never resolves a data repo to find the socket,
// so an invocation from any cwd reaches the one running daemon. (The daemon
// owns the data repo; the CLI only sends RPCs.)
func getClient() *ipc.Client {
	return ipc.NewClient(config.ExpandPath(model.DefaultSocketPath))
}
