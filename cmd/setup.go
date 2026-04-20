package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fyang0507/sundial/internal/config"
	"github.com/fyang0507/sundial/internal/model"
	"github.com/fyang0507/sundial/internal/scaffold"
	"github.com/fyang0507/sundial/internal/version"
	"github.com/spf13/cobra"
)

var setupDataRepoFlag string

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Scaffold sundial's side of the data repo (config, workspace marker, skills)",
	Long: `Scaffold sundial in the data repo:
  - resolve data_repo (--data-repo / SUNDIAL_DATA_REPO / sundial.config.dev.yaml / .agents/workspace.yaml walk-up)
  - write .agents/workspace.yaml with tools.sundial.version stamped
  - create <data_repo>/sundial/config.yaml from template if missing
  - sync embedded skills into <data_repo>/.agents/skills/sundial/

Idempotent — safe to re-run.`,
	Run: runSetup,
}

func init() {
	rootCmd.AddCommand(setupCmd)
	setupCmd.Flags().StringVar(&setupDataRepoFlag, "data-repo", "", "data repo path (overrides SUNDIAL_DATA_REPO and walk-up)")
}

type setupResult struct {
	DataRepo      string `json:"data_repo"`
	Source        string `json:"source"`
	Workspace     string `json:"workspace"`
	Config        string `json:"config"`
	ConfigCreated bool   `json:"config_created"`
	Skills        string `json:"skills"`
	Version       string `json:"version"`
}

func runSetup(cmd *cobra.Command, args []string) {
	var dataRepo string
	var source config.ResolveSource

	if setupDataRepoFlag != "" {
		dataRepo = config.ExpandPath(setupDataRepoFlag)
		source = config.ResolveSourceFlag
	} else {
		res, err := config.ResolveDataRepo()
		if err != nil {
			if errors.Is(err, model.ErrDataRepoNotResolved) {
				fmt.Fprintln(os.Stderr, "Error: data repo not resolved")
				fmt.Fprintln(os.Stderr, "  hint: pass --data-repo <path>, set SUNDIAL_DATA_REPO, or run from a directory under one with .agents/workspace.yaml")
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
			os.Exit(1)
		}
		dataRepo = res.DataRepo
		source = res.Source
	}

	info, err := os.Stat(dataRepo)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: data repo path does not exist or is not a directory: %s\n", dataRepo)
		os.Exit(1)
	}

	// 1. Stamp workspace.yaml.
	if err := config.StampSundialInWorkspace(dataRepo, version.Version); err != nil {
		fmt.Fprintf(os.Stderr, "Error stamping workspace.yaml: %s\n", err)
		os.Exit(1)
	}

	// 2. Scaffold sundial/config.yaml if missing.
	cfgPath := config.ConfigPath(dataRepo)
	configCreated := false
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating sundial/ dir: %s\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(cfgPath, []byte(scaffold.ConfigTemplate), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %s\n", cfgPath, err)
			os.Exit(1)
		}
		configCreated = true
	} else if err != nil {
		fmt.Fprintf(os.Stderr, "Error inspecting %s: %s\n", cfgPath, err)
		os.Exit(1)
	}

	// 3. Ensure schedules dir exists (so the daemon doesn't race on first fire).
	if err := os.MkdirAll(filepath.Join(dataRepo, "sundial", "schedules"), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating schedules dir: %s\n", err)
		os.Exit(1)
	}

	// 4. Sync skills.
	if err := scaffold.CopySkills(dataRepo); err != nil {
		fmt.Fprintf(os.Stderr, "Error syncing skills: %s\n", err)
		os.Exit(1)
	}

	result := setupResult{
		DataRepo:      dataRepo,
		Source:        string(source),
		Workspace:     filepath.Join(dataRepo, config.WorkspaceMarkerRel),
		Config:        cfgPath,
		ConfigCreated: configCreated,
		Skills:        filepath.Join(dataRepo, ".agents", "skills", "sundial"),
		Version:       version.Version,
	}

	if jsonOutput {
		out, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(out))
		return
	}

	fmt.Println("sundial setup")
	fmt.Println()
	fmt.Printf("  data_repo: %s (source: %s)\n", result.DataRepo, result.Source)
	fmt.Printf("  workspace: %s\n", result.Workspace)
	if configCreated {
		fmt.Printf("  config:    %s (created from template)\n", result.Config)
	} else {
		fmt.Printf("  config:    %s (already present — left unchanged)\n", result.Config)
	}
	fmt.Printf("  skills:    %s\n", result.Skills)
	fmt.Printf("  version:   %s\n", result.Version)
	fmt.Println()
	fmt.Println("next: run `make start` (dev) or `sundial install` to register the daemon with launchd")
}
