package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var jsonOutput bool

var rootCmd = &cobra.Command{
	Use:   "sundial",
	Short: "Agent-first CLI scheduler with cron and solar triggers",
	Long: `Sundial is a lightweight, agent-first CLI scheduler that supports both
static (cron) and dynamic (solar) schedules on macOS.

Schedules are managed by a background daemon (launchd) and persisted
to a data repo for portability.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Running with no subcommand prints status summary + help
		fmt.Println("sundial — agent-first CLI scheduler")
		fmt.Println()
		cmd.Help()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "output in JSON format")
}
