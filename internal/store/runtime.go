package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fyang0507/sundial/internal/model"
)

// RuntimeStore handles file I/O for runtime state (local JSON files).
type RuntimeStore struct {
	statePath string
}

// NewRuntimeStore creates a RuntimeStore rooted at the given state directory path.
func NewRuntimeStore(statePath string) *RuntimeStore {
	return &RuntimeStore{statePath: statePath}
}

// EnsureDir creates the state directory if it does not exist.
func (s *RuntimeStore) EnsureDir() error {
	return os.MkdirAll(s.statePath, 0755)
}

// Write atomically writes a RuntimeState to its JSON file.
func (s *RuntimeStore) Write(rs *model.RuntimeState) error {
	data, err := json.MarshalIndent(rs, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime state: %w", err)
	}
	data = append(data, '\n')

	dest := s.filePath(rs.ID)
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

// Read reads a RuntimeState from its JSON file by schedule ID.
func (s *RuntimeStore) Read(id string) (*model.RuntimeState, error) {
	data, err := os.ReadFile(s.filePath(id))
	if err != nil {
		return nil, fmt.Errorf("read runtime state %s: %w", id, err)
	}
	var rs model.RuntimeState
	if err := json.Unmarshal(data, &rs); err != nil {
		return nil, fmt.Errorf("unmarshal runtime state %s: %w", id, err)
	}
	return &rs, nil
}

// List returns all runtime states matching sch_*.json in the state directory.
// Returns an empty slice (not an error) if the directory does not exist.
func (s *RuntimeStore) List() ([]*model.RuntimeState, error) {
	pattern := filepath.Join(s.statePath, "sch_*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob runtime states: %w", err)
	}
	if matches == nil {
		return []*model.RuntimeState{}, nil
	}

	result := make([]*model.RuntimeState, 0, len(matches))
	for _, path := range matches {
		id := strings.TrimSuffix(filepath.Base(path), ".json")
		rs, err := s.Read(id)
		if err != nil {
			return nil, err
		}
		result = append(result, rs)
	}
	return result, nil
}

// Delete removes the runtime state file for the given schedule ID.
// Returns nil if the file does not exist.
func (s *RuntimeStore) Delete(id string) error {
	err := os.Remove(s.filePath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete runtime state %s: %w", id, err)
	}
	return nil
}

func (s *RuntimeStore) filePath(id string) string {
	return filepath.Join(s.statePath, id+".json")
}
