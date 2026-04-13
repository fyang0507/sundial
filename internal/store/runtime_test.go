package store

import (
	"testing"
	"time"

	"github.com/fyang0507/sundial/internal/model"
)

func makeRuntimeState(id string) *model.RuntimeState {
	return &model.RuntimeState{
		ID:         id,
		NextFireAt: time.Date(2025, 1, 16, 9, 0, 0, 0, time.UTC),
		FireCount:  5,
	}
}

func TestRuntimeStore_WriteRead(t *testing.T) {
	dir := t.TempDir()
	s := NewRuntimeStore(dir)
	if err := s.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	want := makeRuntimeState("sch_abc123")
	firedAt := time.Date(2025, 1, 15, 9, 0, 0, 0, time.UTC)
	want.LastFiredAt = &firedAt
	exitCode := 0
	want.LastExitCode = &exitCode

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
	if !got.NextFireAt.Equal(want.NextFireAt) {
		t.Errorf("NextFireAt = %v, want %v", got.NextFireAt, want.NextFireAt)
	}
	if got.LastFiredAt == nil || !got.LastFiredAt.Equal(*want.LastFiredAt) {
		t.Errorf("LastFiredAt = %v, want %v", got.LastFiredAt, want.LastFiredAt)
	}
	if got.LastExitCode == nil || *got.LastExitCode != *want.LastExitCode {
		t.Errorf("LastExitCode = %v, want %v", got.LastExitCode, want.LastExitCode)
	}
	if got.FireCount != want.FireCount {
		t.Errorf("FireCount = %d, want %d", got.FireCount, want.FireCount)
	}
}

func TestRuntimeStore_List(t *testing.T) {
	dir := t.TempDir()
	s := NewRuntimeStore(dir)
	if err := s.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	ids := []string{"sch_001", "sch_002"}
	for _, id := range ids {
		if err := s.Write(makeRuntimeState(id)); err != nil {
			t.Fatalf("Write %s: %v", id, err)
		}
	}

	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d items, want 2", len(got))
	}
}

func TestRuntimeStore_Delete(t *testing.T) {
	dir := t.TempDir()
	s := NewRuntimeStore(dir)
	if err := s.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	rs := makeRuntimeState("sch_del")
	if err := s.Write(rs); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Delete existing file
	if err := s.Delete("sch_del"); err != nil {
		t.Fatalf("Delete existing: %v", err)
	}

	// Verify it's gone
	_, err := s.Read("sch_del")
	if err == nil {
		t.Fatal("Read after delete should return error")
	}
}

func TestRuntimeStore_DeleteNonExistent(t *testing.T) {
	dir := t.TempDir()
	s := NewRuntimeStore(dir)
	if err := s.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	// Delete non-existent file should not error
	if err := s.Delete("sch_nonexistent"); err != nil {
		t.Fatalf("Delete non-existent: %v", err)
	}
}
