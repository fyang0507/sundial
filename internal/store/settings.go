package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fyang0507/sundial/internal/model"
)

// settingsFileName is the single JSON file holding daemon-wide runtime settings
// (currently just the active-hours window) inside the local state directory.
const settingsFileName = "settings.json"

// SettingsStore handles file I/O for the daemon-wide runtime Settings. It lives
// in the LOCAL state directory (not the data repo): active hours are
// machine-local policy that follows the host timezone, and keeping them out of
// git means `sundial set-active-hours` is a fast, always-succeeds toggle with no
// commit/push preconditions.
type SettingsStore struct {
	statePath string
}

// NewSettingsStore creates a SettingsStore rooted at the given state directory.
func NewSettingsStore(statePath string) *SettingsStore {
	return &SettingsStore{statePath: statePath}
}

// EnsureDir creates the state directory if it does not exist.
func (s *SettingsStore) EnsureDir() error {
	return os.MkdirAll(s.statePath, 0755)
}

// Read loads the persisted Settings. A missing file is not an error — it returns
// zero-value Settings (no active-hours window), the fresh-install default.
func (s *SettingsStore) Read() (*model.Settings, error) {
	data, err := os.ReadFile(s.filePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &model.Settings{}, nil
		}
		return nil, fmt.Errorf("read settings: %w", err)
	}
	var st model.Settings
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("unmarshal settings: %w", err)
	}
	return &st, nil
}

// Write atomically persists the Settings.
func (s *SettingsStore) Write(st *model.Settings) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	data = append(data, '\n')

	dest := s.filePath()
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write temp settings file: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename temp settings file: %w", err)
	}
	return nil
}

func (s *SettingsStore) filePath() string {
	return filepath.Join(s.statePath, settingsFileName)
}
