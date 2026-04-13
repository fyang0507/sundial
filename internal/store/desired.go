package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fyang0507/sundial/internal/model"
)

// DesiredStore handles file I/O for desired state (data repo JSON files).
type DesiredStore struct {
	dataRepoPath string
}

// NewDesiredStore creates a DesiredStore rooted at the given data repo path.
func NewDesiredStore(dataRepoPath string) *DesiredStore {
	return &DesiredStore{dataRepoPath: dataRepoPath}
}

func (s *DesiredStore) schedulesDir() string {
	return filepath.Join(s.dataRepoPath, "sundial", "schedules")
}

// EnsureDir creates the schedules directory if it does not exist.
func (s *DesiredStore) EnsureDir() error {
	return os.MkdirAll(s.schedulesDir(), 0755)
}

// Write atomically writes a DesiredState to its JSON file.
func (s *DesiredStore) Write(ds *model.DesiredState) error {
	data, err := json.MarshalIndent(ds, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal desired state: %w", err)
	}
	data = append(data, '\n')

	dest := s.FilePath(ds.ID)
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

// Read reads a DesiredState from its JSON file by schedule ID.
func (s *DesiredStore) Read(id string) (*model.DesiredState, error) {
	data, err := os.ReadFile(s.FilePath(id))
	if err != nil {
		return nil, fmt.Errorf("read desired state %s: %w", id, err)
	}
	var ds model.DesiredState
	if err := json.Unmarshal(data, &ds); err != nil {
		return nil, fmt.Errorf("unmarshal desired state %s: %w", id, err)
	}
	return &ds, nil
}

// List returns all desired states matching sch_*.json in the schedules directory.
// Returns an empty slice (not an error) if the directory does not exist.
func (s *DesiredStore) List() ([]*model.DesiredState, error) {
	pattern := filepath.Join(s.schedulesDir(), "sch_*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob desired states: %w", err)
	}
	if matches == nil {
		return []*model.DesiredState{}, nil
	}

	result := make([]*model.DesiredState, 0, len(matches))
	for _, path := range matches {
		id := strings.TrimSuffix(filepath.Base(path), ".json")
		ds, err := s.Read(id)
		if err != nil {
			return nil, err
		}
		result = append(result, ds)
	}
	return result, nil
}

// FilePath returns the full filesystem path for a schedule ID.
func (s *DesiredStore) FilePath(id string) string {
	return filepath.Join(s.schedulesDir(), id+".json")
}
