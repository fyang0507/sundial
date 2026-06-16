package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/fyang0507/sundial/internal/config"
	"github.com/fyang0507/sundial/internal/format"
	"github.com/fyang0507/sundial/internal/ipc"
	"github.com/fyang0507/sundial/internal/model"
	"github.com/spf13/cobra"
)

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check daemon status and configuration",
	Long:  `Report whether the daemon is running and the parameters it was started with.`,
	Run:   runHealth,
}

func init() {
	rootCmd.AddCommand(healthCmd)
}

func runHealth(cmd *cobra.Command, args []string) {
	// Ask the daemon over the well-known socket — it is the source of truth
	// for all health checks (including which data repo it is attached to).
	var daemonResult model.HealthResult
	client := ipc.NewClient(config.ExpandPath(model.DefaultSocketPath))
	if err := client.Call(model.MethodHealth, nil, &daemonResult); err == nil {
		fmt.Println(format.FormatHealthResult(&daemonResult, jsonOutput))
		return
	}

	// Daemon unreachable.
	if jsonOutput {
		out, _ := json.Marshal(map[string]any{"daemon_running": false})
		fmt.Println(string(out))
	} else {
		fmt.Println("sundial health")
		fmt.Println()
		fmt.Println("daemon: not running")
		fmt.Println()
		fmt.Println("hint: run 'make start' from the sundial repo to start the daemon")
	}
}
