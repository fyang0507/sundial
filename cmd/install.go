package cmd

import (
	"fmt"
	"os"

	"github.com/fyang0507/sundial/internal/config"
	"github.com/fyang0507/sundial/internal/launchd"
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
	// Load and validate config.
	cfgPath, err := config.FindConfigPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %s\n", err)
		os.Exit(1)
	}

	if err := config.Validate(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid config: %s\n", err)
		os.Exit(1)
	}

	// Resolve binary path.
	binPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving executable path: %s\n", err)
		os.Exit(1)
	}

	// Build plist config.
	plistCfg := launchd.PlistConfig{
		Label:        launchd.Label,
		BinaryPath:   binPath,
		ConfigPath:   cfgPath,
		LogPath:      cfg.Daemon.LogFile,
		DataRepoPath: cfg.DataRepo,
	}

	// Install.
	if err := launchd.Install(plistCfg, launchd.DefaultRunner()); err != nil {
		fmt.Fprintf(os.Stderr, "Error installing launchd service: %s\n", err)
		os.Exit(1)
	}

	fmt.Printf("Installed launchd service: %s\n", launchd.PlistPath())
	fmt.Println("The daemon will start automatically on login.")
}
