package cmd

import (
	"fmt"
	"os"

	"github.com/fyang0507/sundial/internal/format"
	"github.com/fyang0507/sundial/internal/model"
	"github.com/spf13/cobra"
)

var removeCmd = &cobra.Command{
	Use:   "remove <id>",
	Short: "Remove a schedule",
	Long:  `Remove a schedule by ID, or remove all schedules with --all --yes.`,
	Run:   runRemove,
}

var (
	removeAll bool
	removeYes bool
)

func init() {
	rootCmd.AddCommand(removeCmd)

	removeCmd.Flags().BoolVar(&removeAll, "all", false, "remove all schedules")
	removeCmd.Flags().BoolVar(&removeYes, "yes", false, "confirm dangerous operations")
}

func runRemove(cmd *cobra.Command, args []string) {
	if removeAll {
		if !removeYes {
			fmt.Fprintln(os.Stderr, "Error: --all requires --yes flag for safety")
			os.Exit(1)
		}
	} else if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: schedule ID required (or use --all --yes)")
		os.Exit(1)
	}

	params := model.RemoveParams{
		All: removeAll,
	}
	if !removeAll && len(args) > 0 {
		params.ID = args[0]
	}

	client, err := getClient()
	if err != nil {
		fmt.Println(format.FormatError(err.Error(), jsonOutput))
		os.Exit(1)
	}

	var result model.RemoveResult
	if err := client.Call(model.MethodRemove, params, &result); err != nil {
		fmt.Println(format.FormatError(err.Error(), jsonOutput))
		os.Exit(1)
	}

	fmt.Println(format.FormatRemoveResult(&result, jsonOutput))
}
