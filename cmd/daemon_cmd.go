package cmd

import (
	"fmt"
	"os"

	"github.com/fyang0507/sundial/internal/config"
	"github.com/fyang0507/sundial/internal/daemon"
	"github.com/fyang0507/sundial/internal/model"
	"github.com/spf13/cobra"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run the scheduler daemon (called by launchd)",
	Long:  `Start the Sundial daemon process. This is typically invoked by launchd, not directly by users.`,
	Run:   runDaemon,
}

var daemonDataRepoFlag string

func init() {
	rootCmd.AddCommand(daemonCmd)

	daemonCmd.Flags().StringVar(&daemonDataRepoFlag, "data-repo", "", "path to the data repo (overrides SUNDIAL_DATA_REPO and walk-up)")
}

func runDaemon(cmd *cobra.Command, args []string) {
	// Resolve: explicit --data-repo wins, otherwise the standard resolver.
	var cfg *model.Config
	var err error
	if daemonDataRepoFlag != "" {
		cfg, _, err = config.LoadForDataRepo(daemonDataRepoFlag)
	} else {
		cfg, _, err = config.LoadAndResolve()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
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
