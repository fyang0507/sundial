package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fyang0507/sundial/internal/model"
)

// RunLogStore handles file I/O for per-schedule run logs (JSONL files).
type RunLogStore struct {
	logsPath string
}

// NewRunLogStore creates a RunLogStore rooted at the given logs directory path.
func NewRunLogStore(logsPath string) *RunLogStore {
	return &RunLogStore{logsPath: logsPath}
}

// EnsureDir creates the logs directory if it does not exist.
func (s *RunLogStore) EnsureDir() error {
	return os.MkdirAll(s.logsPath, 0755)
}

// Append appends a single RunLogEntry as a JSON line to the schedule's log file.
func (s *RunLogStore) Append(entry *model.RunLogEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal run log entry: %w", err)
	}

	f, err := os.OpenFile(s.filePath(entry.ScheduleID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open run log file: %w", err)
	}
	defer f.Close()

	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write run log entry: %w", err)
	}
	return nil
}

// Read reads all RunLogEntry records from the schedule's JSONL file.
// Returns an empty slice if the file does not exist.
func (s *RunLogStore) Read(id string) ([]*model.RunLogEntry, error) {
	f, err := os.Open(s.filePath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []*model.RunLogEntry{}, nil
		}
		return nil, fmt.Errorf("open run log file: %w", err)
	}
	defer f.Close()

	var entries []*model.RunLogEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry model.RunLogEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("unmarshal run log entry: %w", err)
		}
		entries = append(entries, &entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan run log file: %w", err)
	}
	if entries == nil {
		entries = []*model.RunLogEntry{}
	}
	return entries, nil
}

// MissedSince counts missed fires for a schedule since the given time.
// For "miss" entries, each counts as 1. For "miss_summary" entries, the Count field is added.
func (s *RunLogStore) MissedSince(id string, since time.Time) (int, error) {
	entries, err := s.Read(id)
	if err != nil {
		return 0, err
	}

	total := 0
	for _, e := range entries {
		if !e.Timestamp.After(since) {
			continue
		}
		switch e.Type {
		case model.LogTypeMiss:
			total++
		case model.LogTypeMissSummary:
			total += e.Count
		}
	}
	return total, nil
}

func (s *RunLogStore) filePath(id string) string {
	return filepath.Join(s.logsPath, id+".jsonl")
}
