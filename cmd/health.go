package cmd

import (
	"fmt"
	"os"

	"github.com/fyang0507/sundial/internal/config"
	"github.com/fyang0507/sundial/internal/format"
	"github.com/fyang0507/sundial/internal/ipc"
	"github.com/fyang0507/sundial/internal/model"
	"github.com/spf13/cobra"
)

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check daemon and system health",
	Long:  `Run health checks against the daemon, config, and data repo.`,
	Run:   runHealth,
}

func init() {
	rootCmd.AddCommand(healthCmd)
}

func runHealth(cmd *cobra.Command, args []string) {
	// Try to load config for local checks.
	cfgPath, cfgErr := config.FindConfigPath()
	var cfg *model.Config
	configValid := false
	dataRepoOK := false

	if cfgErr == nil {
		loaded, loadErr := config.Load(cfgPath)
		if loadErr == nil {
			cfg = loaded
			if validateErr := config.Validate(cfg); validateErr == nil {
				configValid = true
				// Check data repo exists.
				if _, statErr := os.Stat(cfg.DataRepo); statErr == nil {
					dataRepoOK = true
				}
			}
		}
	}

	// Try to reach daemon.
	var daemonResult model.HealthResult
	daemonReachable := false

	if cfg != nil {
		client := ipc.NewClient(cfg.Daemon.SocketPath)
		if err := client.Call(model.MethodHealth, nil, &daemonResult); err == nil {
			daemonReachable = true
		}
	}

	if daemonReachable {
		// Use the full result from the daemon.
		fmt.Println(format.FormatHealthResult(&daemonResult, jsonOutput))
		return
	}

	// Daemon not reachable — build a local-only health result.
	result := model.HealthResult{
		Healthy:       false,
		DaemonRunning: false,
		ConfigValid:   configValid,
		DataRepoOK:    dataRepoOK,
	}

	result.Checks = append(result.Checks, model.HealthCheck{
		Name:    "daemon",
		Status:  "error",
		Message: "not running",
	})

	if configValid {
		result.Checks = append(result.Checks, model.HealthCheck{
			Name:   "config",
			Status: "ok",
		})
	} else {
		msg := "not found"
		if cfgErr == nil {
			msg = "invalid"
		}
		result.Checks = append(result.Checks, model.HealthCheck{
			Name:    "config",
			Status:  "error",
			Message: msg,
		})
	}

	if dataRepoOK {
		result.Checks = append(result.Checks, model.HealthCheck{
			Name:   "data_repo",
			Status: "ok",
		})
	} else {
		msg := "not found"
		if configValid {
			msg = "path does not exist"
		}
		result.Checks = append(result.Checks, model.HealthCheck{
			Name:    "data_repo",
			Status:  "error",
			Message: msg,
		})
	}

	fmt.Println(format.FormatHealthResult(&result, jsonOutput))
}
