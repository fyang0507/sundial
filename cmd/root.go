package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var jsonOutput bool

var rootCmd = &cobra.Command{
	Use:   "sundial",
	Short: "Agent-native CLI scheduler with cron, solar, poll, and at triggers",
	Long: `Sundial is a lightweight, agent-native CLI scheduler for macOS.
It supports cron, solar, poll, and one-off at triggers.

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
