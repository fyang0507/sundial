package cmd

import (
	"fmt"
	"os"

	"github.com/fyang0507/sundial/internal/config"
	"github.com/fyang0507/sundial/internal/launchd"
	"github.com/spf13/cobra"
)

var (
	installConfigFlag   string
	installDataRepoFlag string
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the Sundial daemon as a launchd service",
	Long:  `Generate and install a launchd plist so the Sundial daemon starts automatically on login.`,
	Run:   runInstall,
}

func init() {
	rootCmd.AddCommand(installCmd)
	installCmd.Flags().StringVar(&installConfigFlag, "config", "", "path to sundial.config.yaml (overrides SUNDIAL_CONFIG env)")
	installCmd.Flags().StringVar(&installDataRepoFlag, "data-repo", "", "path to the data repo (overrides data_repo_path in the config file)")
}

func runInstall(cmd *cobra.Command, args []string) {
	// Locate and load the single config file. The resolved absolute config path
	// is baked into the launchd plist's ProgramArguments via --config.
	cfg, cfgPath, err := config.LoadAndResolve(installConfigFlag, installDataRepoFlag)
	if err != nil {
		if config.IsResolveError(err) {
			fmt.Fprintln(os.Stderr, "Error: config file not located")
			fmt.Fprintln(os.Stderr, "  hint: pass --config <path> or set SUNDIAL_CONFIG")
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
		Label:          launchd.Label,
		BinaryPath:     binPath,
		LogPath:        cfg.Daemon.LogFile,
		DataRepoPath:   cfg.DataRepo,
		ConfigFilePath: cfgPath,
	}

	if err := launchd.Install(plistCfg, launchd.DefaultRunner()); err != nil {
		fmt.Fprintf(os.Stderr, "Error installing launchd service: %s\n", err)
		os.Exit(1)
	}

	fmt.Printf("Installed launchd service: %s\n", launchd.PlistPath())
	fmt.Println("The daemon will start automatically on login.")
}
