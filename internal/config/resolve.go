package config

import (
	"errors"

	"github.com/fyang0507/sundial/internal/model"
)

// WorkspaceMarkerRel is the relative path, from the data repo root, of the
// workspace marker file shared across the agent stack.
const WorkspaceMarkerRel = ".agents/workspace.yaml"

// ResolveSource is the origin of the resolved data repo path.
type ResolveSource string

const (
	ResolveSourceConfig ResolveSource = "config" // data_repo_path in the config file
	ResolveSourceEnv    ResolveSource = "env"    // SUNDIAL_DATA_REPO override
	ResolveSourceFlag   ResolveSource = "flag"   // explicit --data-repo argument
)

// ReadDataRepoPath strictly decodes the single config file at path and returns
// its data_repo_path (tilde-expanded). Exposed so external tooling (the
// Makefile shim) can share the parser and read the same data_repo_path the
// daemon would.
func ReadDataRepoPath(path string) (string, error) {
	cfg, err := decodeConfigFile(path)
	if err != nil {
		return "", err
	}
	if cfg.DataRepo == "" {
		return "", errors.New(path + ": missing or has no data_repo_path")
	}
	return ExpandPath(cfg.DataRepo), nil
}

// IsResolveError reports whether err is a config/data-repo resolution failure.
func IsResolveError(err error) bool {
	return errors.Is(err, model.ErrDataRepoNotResolved) || errors.Is(err, model.ErrConfigNotResolved)
}
