package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/fyang0507/sundial/internal/config"
	"github.com/fyang0507/sundial/internal/launchd"
	"github.com/fyang0507/sundial/internal/model"
	"github.com/spf13/cobra"
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the Sundial daemon as a launchd service",
	Long:  `Generate and install a launchd plist so the Sundial daemon starts automatically on login.`,
	Run:   runInstall,
}

func init() {
	rootCmd.AddCommand(installCmd)
}

func runInstall(cmd *cobra.Command, args []string) {
	// Resolve data repo and load config.
	cfg, _, err := config.LoadAndResolve()
	if err != nil {
		if errors.Is(err, model.ErrDataRepoNotResolved) {
			fmt.Fprintln(os.Stderr, "Error: data repo not resolved")
			fmt.Fprintln(os.Stderr, "  hint: run `sundial setup --data-repo <path>` first, or set SUNDIAL_DATA_REPO")
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}

	if err := config.Validate(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid config: %s\n", err)
		os.Exit(1)
	}

	binPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving executable path: %s\n", err)
		os.Exit(1)
	}

	plistCfg := launchd.PlistConfig{
		Label:        launchd.Label,
		BinaryPath:   binPath,
		LogPath:      cfg.Daemon.LogFile,
		DataRepoPath: cfg.DataRepo,
	}

	if err := launchd.Install(plistCfg, launchd.DefaultRunner()); err != nil {
		fmt.Fprintf(os.Stderr, "Error installing launchd service: %s\n", err)
		os.Exit(1)
	}

	fmt.Printf("Installed launchd service: %s\n", launchd.PlistPath())
	fmt.Println("The daemon will start automatically on login.")
}
