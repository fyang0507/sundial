package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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

// configTemplate is the minimal config written when bootstrapping.
const configTemplate = `data_repo: ""   # REQUIRED — set this to the path of your git data repo
`

func runInstall(cmd *cobra.Command, args []string) {
	// Load and validate config.
	cfgPath, err := config.FindConfigPath()
	if errors.Is(err, model.ErrConfigNotFound) {
		bootstrapConfig()
	} else if err != nil {
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

// bootstrapConfig creates a minimal config template at ~/.config/sundial/config.yaml
// and exits with code 1, asking the user to fill in data_repo.
func bootstrapConfig() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot determine home directory: %s\n", err)
		os.Exit(1)
	}

	target := filepath.Join(home, ".config", "sundial", "config.yaml")

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot create config directory: %s\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(target, []byte(configTemplate), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot write config template: %s\n", err)
		os.Exit(1)
	}

	fmt.Printf("Config template created at %s\n", target)
	fmt.Println("Set data_repo to your git data repo path, then re-run: sundial install")
	os.Exit(1)
}
