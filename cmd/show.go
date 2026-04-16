package cmd

import (
	"fmt"

	"github.com/fyang0507/sundial/internal/format"
	"github.com/fyang0507/sundial/internal/model"
	"github.com/spf13/cobra"
)

var showCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show details of a schedule",
	Long:  `Show detailed information about a specific schedule by ID.`,
	Args:  cobra.ExactArgs(1),
	Run:   runShow,
}

func init() {
	rootCmd.AddCommand(showCmd)
}

func runShow(cmd *cobra.Command, args []string) {
	params := model.ShowParams{
		ID: args[0],
	}

	client, err := getClient()
	if err != nil {
		handleClientError(err)
	}

	var result model.ShowResult
	if err := client.Call(model.MethodShow, params, &result); err != nil {
		handleCallError(err)
	}

	fmt.Println(format.FormatShowResult(&result, jsonOutput))
}
