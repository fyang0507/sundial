package store

import (
	"testing"
	"time"

	"github.com/fyang0507/sundial/internal/model"
)

func makeDesiredState(id, name string) *model.DesiredState {
	return &model.DesiredState{
		ID:        id,
		Name:      name,
		CreatedAt: time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
		Trigger: model.TriggerConfig{
			Type: model.TriggerTypeCron,
			Cron: "0 9 * * *",
		},
		Command: "echo hello",
		Status:  model.StatusActive,
	}
}

func TestDesiredStore_WriteRead(t *testing.T) {
	dir := t.TempDir()
	s := NewDesiredStore(dir)
	if err := s.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	want := makeDesiredState("sch_abc123", "test schedule")
	if err := s.Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := s.Read("sch_abc123")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if got.ID != want.ID {
		t.Errorf("ID = %q, want %q", got.ID, want.ID)
	}
	if got.Name != want.Name {
		t.Errorf("Name = %q, want %q", got.Name, want.Name)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
	if got.Trigger.Cron != want.Trigger.Cron {
		t.Errorf("Trigger.Cron = %q, want %q", got.Trigger.Cron, want.Trigger.Cron)
	}
	if got.Command != want.Command {
		t.Errorf("Command = %q, want %q", got.Command, want.Command)
	}
	if got.Status != want.Status {
		t.Errorf("Status = %q, want %q", got.Status, want.Status)
	}
}

func TestDesiredStore_ListMultiple(t *testing.T) {
	dir := t.TempDir()
	s := NewDesiredStore(dir)
	if err := s.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	ids := []string{"sch_001", "sch_002", "sch_003"}
	for _, id := range ids {
		ds := makeDesiredState(id, "schedule "+id)
		if err := s.Write(ds); err != nil {
			t.Fatalf("Write %s: %v", id, err)
		}
	}

	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("List returned %d items, want 3", len(got))
	}

	found := make(map[string]bool)
	for _, ds := range got {
		found[ds.ID] = true
	}
	for _, id := range ids {
		if !found[id] {
			t.Errorf("List missing ID %s", id)
		}
	}
}

func TestDesiredStore_ListEmptyDir(t *testing.T) {
	dir := t.TempDir()
	s := NewDesiredStore(dir)
	// Don't create the directory — List should return empty slice, not error.

	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("List returned %d items, want 0", len(got))
	}
}

func TestDesiredStore_FilePath(t *testing.T) {
	s := NewDesiredStore("/tmp/repo")
	want := "/tmp/repo/sundial/schedules/sch_abc.json"
	got := s.FilePath("sch_abc")
	if got != want {
		t.Errorf("FilePath = %q, want %q", got, want)
	}
}
