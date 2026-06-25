package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fyang0507/sundial/internal/config"
	"github.com/fyang0507/sundial/internal/scaffold"
	"github.com/fyang0507/sundial/internal/version"
	"github.com/spf13/cobra"
)

var setupDataRepoFlag string

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Scaffold sundial's side of the data repo (workspace marker, agent skill symlink)",
	Long: `Scaffold sundial in the data repo:
  - resolve data_repo (--data-repo / SUNDIAL_DATA_REPO)
  - write .agents/workspace.yaml with tools.sundial.version stamped
  - install <data_repo>/.agents/skills/sundial as an agent skill symlink to the source skill tree

Daemon configuration lives in the sundial source repo's sundial.config.yaml,
not in the data repo — setup no longer writes any config there.

Idempotent — safe to re-run.`,
	Run: runSetup,
}

func init() {
	rootCmd.AddCommand(setupCmd)
	setupCmd.Flags().StringVar(&setupDataRepoFlag, "data-repo", "", "data repo path (overrides SUNDIAL_DATA_REPO)")
}

type setupResult struct {
	DataRepo  string `json:"data_repo"`
	Source    string `json:"source"`
	Workspace string `json:"workspace"`
	Skills    string `json:"skills"`
	Version   string `json:"version"`
}

func runSetup(cmd *cobra.Command, args []string) {
	var dataRepo string
	var source config.ResolveSource

	switch {
	case setupDataRepoFlag != "":
		dataRepo = config.ExpandPath(setupDataRepoFlag)
		source = config.ResolveSourceFlag
	case os.Getenv("SUNDIAL_DATA_REPO") != "":
		dataRepo = config.ExpandPath(os.Getenv("SUNDIAL_DATA_REPO"))
		source = config.ResolveSourceEnv
	default:
		fmt.Fprintln(os.Stderr, "Error: data repo not resolved")
		fmt.Fprintln(os.Stderr, "  hint: pass --data-repo <path> or set SUNDIAL_DATA_REPO")
		os.Exit(1)
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

	// 2. Ensure schedules dir exists (so the daemon doesn't race on first fire).
	if err := os.MkdirAll(filepath.Join(dataRepo, "sundial", "schedules"), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating schedules dir: %s\n", err)
		os.Exit(1)
	}

	// 3. Install agent skill symlink.
	if err := scaffold.InstallSkillTree(dataRepo); err != nil {
		fmt.Fprintf(os.Stderr, "Error installing agent skill symlink: %s\n", err)
		os.Exit(1)
	}

	result := setupResult{
		DataRepo:  dataRepo,
		Source:    string(source),
		Workspace: filepath.Join(dataRepo, config.WorkspaceMarkerRel),
		Skills:    filepath.Join(dataRepo, ".agents", "skills", "sundial"),
		Version:   version.Version,
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
	fmt.Printf("  skills:    %s\n", result.Skills)
	fmt.Printf("  version:   %s\n", result.Version)
	fmt.Println()
	fmt.Println("next: run `make start` (dev) or `sundial install --config <path>` to register the daemon with launchd")
}
