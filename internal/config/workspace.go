package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// WorkspaceVersion is the schema version written to .agents/workspace.yaml.
const WorkspaceVersion = 1

// Workspace is the schema of .agents/workspace.yaml — the marker file shared
// across the agent stack (outreach, sundial, relay). Each tool registers
// itself under Tools by name.
type Workspace struct {
	Version int                       `yaml:"version"`
	Tools   map[string]WorkspaceTool  `yaml:"tools,omitempty"`
}

// WorkspaceTool is the per-tool entry in workspace.yaml.
type WorkspaceTool struct {
	Version string `yaml:"version"`
}

// ReadWorkspace reads .agents/workspace.yaml at the given data repo root.
// Returns an empty workspace (no error) if the file does not exist.
func ReadWorkspace(dataRepo string) (*Workspace, error) {
	path := filepath.Join(dataRepo, WorkspaceMarkerRel)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Workspace{Version: WorkspaceVersion}, nil
		}
		return nil, fmt.Errorf("reading workspace.yaml: %w", err)
	}
	var ws Workspace
	if err := yaml.Unmarshal(data, &ws); err != nil {
		return nil, fmt.Errorf("parsing workspace.yaml: %w", err)
	}
	if ws.Version == 0 {
		ws.Version = WorkspaceVersion
	}
	if ws.Tools == nil {
		ws.Tools = map[string]WorkspaceTool{}
	}
	return &ws, nil
}

// WriteWorkspace writes the workspace marker under dataRepo, creating the
// .agents/ directory if necessary.
func WriteWorkspace(dataRepo string, ws *Workspace) error {
	dir := filepath.Join(dataRepo, ".agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	path := filepath.Join(dataRepo, WorkspaceMarkerRel)
	data, err := yaml.Marshal(ws)
	if err != nil {
		return fmt.Errorf("marshaling workspace.yaml: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// StampSundialInWorkspace reads the workspace marker, sets
// tools.sundial.version = version, and writes it back. Idempotent.
func StampSundialInWorkspace(dataRepo, version string) error {
	ws, err := ReadWorkspace(dataRepo)
	if err != nil {
		return err
	}
	if ws.Tools == nil {
		ws.Tools = map[string]WorkspaceTool{}
	}
	ws.Tools["sundial"] = WorkspaceTool{Version: version}
	return WriteWorkspace(dataRepo, ws)
}
