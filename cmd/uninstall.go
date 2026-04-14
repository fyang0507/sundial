package cmd

import (
	"fmt"

	"github.com/fyang0507/sundial/internal/launchd"
	"github.com/spf13/cobra"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall the Sundial daemon from launchd",
	Long:  `Unload the Sundial daemon from launchd and remove the plist file.`,
	Run:   runUninstall,
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
}

func runUninstall(cmd *cobra.Command, args []string) {
	if err := launchd.Uninstall(launchd.DefaultRunner()); err != nil {
		fmt.Printf("Error uninstalling: %s\n", err)
		return
	}

	fmt.Println("Uninstalled launchd service.")
	fmt.Println("The daemon will no longer start on login.")
}
