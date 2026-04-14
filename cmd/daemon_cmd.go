package cmd

import (
	"fmt"
	"os"

	"github.com/fyang0507/sundial/internal/config"
	"github.com/fyang0507/sundial/internal/daemon"
	"github.com/spf13/cobra"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run the scheduler daemon (called by launchd)",
	Long:  `Start the Sundial daemon process. This is typically invoked by launchd, not directly by users.`,
	Run:   runDaemon,
}

var daemonConfigPath string

func init() {
	rootCmd.AddCommand(daemonCmd)

	daemonCmd.Flags().StringVar(&daemonConfigPath, "config", "", "path to config.yaml")
}

func runDaemon(cmd *cobra.Command, args []string) {
	// Load config: use --config flag or discover automatically.
	var cfgPath string
	var err error

	if daemonConfigPath != "" {
		cfgPath = config.ExpandPath(daemonConfigPath)
	} else {
		cfgPath, err = config.FindConfigPath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
			os.Exit(1)
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %s\n", err)
		os.Exit(1)
	}

	if err := config.Validate(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid config: %s\n", err)
		os.Exit(1)
	}

	d, err := daemon.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating daemon: %s\n", err)
		os.Exit(1)
	}

	if err := d.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting daemon: %s\n", err)
		os.Exit(1)
	}

	// Block until the daemon shuts down (signal handling is internal).
	d.Wait()
}
