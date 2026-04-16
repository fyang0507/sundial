package cmd

import (
	"fmt"
	"os"

	"github.com/fyang0507/sundial/internal/format"
	"github.com/fyang0507/sundial/internal/model"
	"github.com/spf13/cobra"
)

var unpauseCmd = &cobra.Command{
	Use:   "unpause <id>",
	Short: "Resume a paused schedule",
	Long:  `Resume a paused schedule so it starts firing again.`,
	Run:   runUnpause,
}

func init() {
	rootCmd.AddCommand(unpauseCmd)
}

func runUnpause(cmd *cobra.Command, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: schedule ID required")
		os.Exit(1)
	}

	params := model.PauseParams{ID: args[0]}

	client, err := getClient()
	if err != nil {
		handleClientError(err)
	}

	var result model.PauseResult
	if err := client.Call(model.MethodUnpause, params, &result); err != nil {
		handleCallError(err)
	}

	fmt.Println(format.FormatPauseResult(&result, jsonOutput))
}
