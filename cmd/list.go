package cmd

import (
	"fmt"
	"os"

	"github.com/fyang0507/sundial/internal/format"
	"github.com/fyang0507/sundial/internal/model"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all schedules",
	Long:  `List all active schedules managed by the daemon.`,
	Run:   runList,
}

func init() {
	rootCmd.AddCommand(listCmd)
}

func runList(cmd *cobra.Command, args []string) {
	client, err := getClient()
	if err != nil {
		fmt.Println(format.FormatError(err.Error(), jsonOutput))
		os.Exit(1)
	}

	var result model.ListResult
	if err := client.Call(model.MethodList, model.ListParams{}, &result); err != nil {
		fmt.Println(format.FormatError(err.Error(), jsonOutput))
		os.Exit(1)
	}

	fmt.Println(format.FormatListResult(&result, jsonOutput))
}
