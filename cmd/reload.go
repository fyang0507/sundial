package cmd

import (
	"fmt"

	"github.com/fyang0507/sundial/internal/model"
	"github.com/spf13/cobra"
)

var reloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "Reload daemon configuration and reconcile schedules",
	Long:  `Tell the daemon to reload its configuration and reconcile all schedules.`,
	Run:   runReload,
}

func init() {
	rootCmd.AddCommand(reloadCmd)
}

func runReload(cmd *cobra.Command, args []string) {
	client, err := getClient()
	if err != nil {
		handleClientError(err)
	}

	var result model.ReloadResult
	if err := client.Call(model.MethodReload, nil, &result); err != nil {
		handleCallError(err)
	}

	fmt.Println(result.Message)
}
