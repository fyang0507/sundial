package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fyang0507/sundial/internal/model"
	"gopkg.in/yaml.v3"
)

// DevYAMLFilename is the name of the dev-local pointer file that sits next to
// the running sundial binary in dev builds.
const DevYAMLFilename = "sundial.config.dev.yaml"

// WorkspaceMarkerRel is the relative path, from the data repo root, of the
// workspace marker file shared across the agent stack.
const WorkspaceMarkerRel = ".agents/workspace.yaml"

// DevYAML is the schema of sundial.config.dev.yaml — a dev-local pointer to
// the data repo. It is not shipped to production; it is only consulted when
// it sits next to the running sundial binary.
type DevYAML struct {
	DataRepoPath string `yaml:"data_repo_path"`
}

// ResolveSource is the origin of the resolved data repo path.
type ResolveSource string

const (
	ResolveSourceEnv       ResolveSource = "env"       // SUNDIAL_DATA_REPO
	ResolveSourceDev       ResolveSource = "dev"       // sundial.config.dev.yaml next to binary
	ResolveSourceWorkspace ResolveSource = "workspace" // .agents/workspace.yaml walk-up from cwd
	ResolveSourceFlag      ResolveSource = "flag"      // explicit --data-repo argument
)

// ResolveResult captures where ResolveDataRepo located the data repo.
type ResolveResult struct {
	DataRepo string
	Source   ResolveSource
}

// ResolveDataRepo returns the data repo path following the documented order:
//
//  1. SUNDIAL_DATA_REPO env var
//  2. sundial.config.dev.yaml adjacent to the running binary (data_repo_path)
//  3. Walk up from cwd looking for .agents/workspace.yaml
//
// Returns model.ErrDataRepoNotResolved with a remediation message otherwise.
func ResolveDataRepo() (ResolveResult, error) {
	if envPath := os.Getenv("SUNDIAL_DATA_REPO"); envPath != "" {
		p := ExpandPath(envPath)
		return ResolveResult{DataRepo: p, Source: ResolveSourceEnv}, nil
	}

	if p, ok := readDevYAMLNextToBinary(); ok {
		return ResolveResult{DataRepo: p, Source: ResolveSourceDev}, nil
	}

	if p, ok := walkUpForWorkspace(); ok {
		return ResolveResult{DataRepo: p, Source: ResolveSourceWorkspace}, nil
	}

	return ResolveResult{}, fmt.Errorf(
		"could not resolve data repo: set SUNDIAL_DATA_REPO, run `sundial setup --data-repo <path>`, or invoke from a directory under one with .agents/workspace.yaml: %w",
		model.ErrDataRepoNotResolved,
	)
}

// readDevYAMLNextToBinary looks for sundial.config.dev.yaml in the directory
// of the running executable and returns its data_repo_path if present.
func readDevYAMLNextToBinary() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	candidate := filepath.Join(filepath.Dir(exe), DevYAMLFilename)
	return readDevYAML(candidate)
}

// readDevYAML parses sundial.config.dev.yaml at path and returns
// data_repo_path (tilde-expanded). Missing file or empty field returns false.
func readDevYAML(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var dev DevYAML
	if err := yaml.Unmarshal(data, &dev); err != nil {
		return "", false
	}
	if dev.DataRepoPath == "" {
		return "", false
	}
	return ExpandPath(dev.DataRepoPath), true
}

// ReadDevYAMLAt parses the dev yaml at path and returns its data_repo_path.
// Exposed so external tooling (e.g. the Makefile shim) can share the parser.
func ReadDevYAMLAt(path string) (string, error) {
	p, ok := readDevYAML(path)
	if !ok {
		return "", fmt.Errorf("%s: missing or has no data_repo_path", path)
	}
	return p, nil
}

// walkUpForWorkspace walks up from cwd looking for .agents/workspace.yaml.
// Returns the parent directory (data repo root) when found.
func walkUpForWorkspace() (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	dir := cwd
	for {
		marker := filepath.Join(dir, WorkspaceMarkerRel)
		if _, err := os.Stat(marker); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// IsResolveError reports whether err is model.ErrDataRepoNotResolved.
func IsResolveError(err error) bool {
	return errors.Is(err, model.ErrDataRepoNotResolved)
}
