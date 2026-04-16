package cmd

import (
	"fmt"
	"os"

	"github.com/fyang0507/sundial/internal/format"
	"github.com/fyang0507/sundial/internal/model"
	"github.com/spf13/cobra"
)

var pauseCmd = &cobra.Command{
	Use:   "pause <id>",
	Short: "Pause a schedule",
	Long:  `Pause an active schedule so it stops firing. Use "sundial unpause" to resume.`,
	Run:   runPause,
}

func init() {
	rootCmd.AddCommand(pauseCmd)
}

func runPause(cmd *cobra.Command, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: schedule ID required")
		os.Exit(1)
	}

	params := model.PauseParams{ID: args[0]}

	client, err := getClient()
	if err != nil {
		fmt.Println(format.FormatError(err.Error(), jsonOutput))
		os.Exit(1)
	}

	var result model.PauseResult
	if err := client.Call(model.MethodPause, params, &result); err != nil {
		fmt.Println(format.FormatError(err.Error(), jsonOutput))
		os.Exit(1)
	}

	fmt.Println(format.FormatPauseResult(&result, jsonOutput))
}
