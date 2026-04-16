package gitops

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/fyang0507/sundial/internal/model"
)

// initTestRepo creates a temporary git repo with one empty commit and returns
// its path. It configures a local user.email and user.name so commits work in
// any environment (including CI with no global git config).
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	return dir
}

func TestCheckRepoPreconditions_Clean(t *testing.T) {
	repo := initTestRepo(t)
	g := NewGitOps(repo)

	if err := g.CheckRepoPreconditions(); err != nil {
		t.Fatalf("expected no error on clean repo, got: %v", err)
	}
}

func TestCheckRepoPreconditions_DetachedHead(t *testing.T) {
	repo := initTestRepo(t)

	cmd := exec.Command("git", "checkout", "--detach")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout --detach failed: %v\n%s", err, out)
	}

	g := NewGitOps(repo)
	err := g.CheckRepoPreconditions()
	if err == nil {
		t.Fatal("expected error for detached HEAD, got nil")
	}
	if !errors.Is(err, model.ErrGitPreconditionFailed) {
		t.Fatalf("expected ErrGitPreconditionFailed, got: %v", err)
	}
	if !strings.Contains(err.Error(), "detached HEAD") {
		t.Fatalf("expected 'detached HEAD' in error message, got: %v", err)
	}
}

func TestCheckRepoPreconditions_RebaseInProgress(t *testing.T) {
	repo := initTestRepo(t)

	// Simulate a rebase in progress by creating the .git/rebase-merge directory.
	if err := os.MkdirAll(filepath.Join(repo, ".git", "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}

	g := NewGitOps(repo)
	err := g.CheckRepoPreconditions()
	if err == nil {
		t.Fatal("expected error for rebase in progress, got nil")
	}
	if !errors.Is(err, model.ErrGitPreconditionFailed) {
		t.Fatalf("expected ErrGitPreconditionFailed, got: %v", err)
	}
	if !strings.Contains(err.Error(), "rebase in progress") {
		t.Fatalf("expected 'rebase in progress' in error message, got: %v", err)
	}
}

func TestCheckRepoPreconditions_MergeInProgress(t *testing.T) {
	repo := initTestRepo(t)

	// Simulate a merge in progress by creating .git/MERGE_HEAD.
	if err := os.WriteFile(filepath.Join(repo, ".git", "MERGE_HEAD"), []byte("abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	g := NewGitOps(repo)
	err := g.CheckRepoPreconditions()
	if err == nil {
		t.Fatal("expected error for merge in progress, got nil")
	}
	if !errors.Is(err, model.ErrGitPreconditionFailed) {
		t.Fatalf("expected ErrGitPreconditionFailed, got: %v", err)
	}
	if !strings.Contains(err.Error(), "merge in progress") {
		t.Fatalf("expected 'merge in progress' in error message, got: %v", err)
	}
}

func TestCheckFilePreconditions_Clean(t *testing.T) {
	repo := initTestRepo(t)

	// Create and commit a file so it is tracked and clean.
	filePath := filepath.Join(repo, "schedule.yaml")
	if err := os.WriteFile(filePath, []byte("name: test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "schedule.yaml"},
		{"commit", "-m", "add schedule"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	g := NewGitOps(repo)
	if err := g.CheckFilePreconditions("schedule.yaml"); err != nil {
		t.Fatalf("expected no error for clean file, got: %v", err)
	}
}

func TestCheckFilePreconditions_Modified(t *testing.T) {
	repo := initTestRepo(t)

	// Create and commit a file, then modify it to create unstaged changes.
	filePath := filepath.Join(repo, "schedule.yaml")
	if err := os.WriteFile(filePath, []byte("name: test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "schedule.yaml"},
		{"commit", "-m", "add schedule"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	// Modify the file
	if err := os.WriteFile(filePath, []byte("name: modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	g := NewGitOps(repo)
	err := g.CheckFilePreconditions("schedule.yaml")
	if err == nil {
		t.Fatal("expected error for modified file, got nil")
	}
	if !strings.Contains(err.Error(), "file has local modifications") {
		t.Fatalf("expected 'file has local modifications' in error, got: %v", err)
	}
}

func TestCommitSchedule(t *testing.T) {
	repo := initTestRepo(t)

	// Write a new file and commit it via CommitSchedule.
	filePath := filepath.Join(repo, "schedule.yaml")
	if err := os.WriteFile(filePath, []byte("name: test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	g := NewGitOps(repo)
	if err := g.CommitSchedule("schedule.yaml", "add test schedule"); err != nil {
		t.Fatalf("CommitSchedule failed: %v", err)
	}

	// Verify git log shows the commit message.
	out, err := runGit(repo, "log", "--oneline", "-1")
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}
	if !strings.Contains(out, "add test schedule") {
		t.Fatalf("expected commit message in log, got: %s", out)
	}
}

func TestCommitSchedule_OnlyTargetFile(t *testing.T) {
	repo := initTestRepo(t)

	// Create and stage a file that should NOT be in the commit.
	otherFile := filepath.Join(repo, "other.txt")
	if err := os.WriteFile(otherFile, []byte("other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "add", "other.txt")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add other.txt failed: %v\n%s", err, out)
	}

	// Create the target file and commit only it.
	targetFile := filepath.Join(repo, "schedule.yaml")
	if err := os.WriteFile(targetFile, []byte("name: target\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	g := NewGitOps(repo)
	if err := g.CommitSchedule("schedule.yaml", "commit only target"); err != nil {
		t.Fatalf("CommitSchedule failed: %v", err)
	}

	// Verify that only schedule.yaml is in the latest commit.
	out, err := runGit(repo, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD")
	if err != nil {
		t.Fatalf("git diff-tree failed: %v", err)
	}
	files := splitLines(out)
	if len(files) != 1 || files[0] != "schedule.yaml" {
		t.Fatalf("expected only schedule.yaml in commit, got: %v", files)
	}

	// Verify that other.txt is still staged (not committed).
	staged, err := runGit(repo, "diff", "--cached", "--name-only")
	if err != nil {
		t.Fatalf("git diff --cached failed: %v", err)
	}
	if !strings.Contains(staged, "other.txt") {
		t.Fatalf("expected other.txt to still be staged, got: %s", staged)
	}
}

// TestCommitSchedule_ConcurrentSameRepo spawns many goroutines each committing
// a distinct file on the same repo to exercise the serialization mutex. Without
// the mutex, concurrent `git commit --only` invocations race on
// .git/index.lock, which is what caused the nested-add IPC timeout (#33).
func TestCommitSchedule_ConcurrentSameRepo(t *testing.T) {
	repo := initTestRepo(t)
	g := NewGitOps(repo)

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)

	for i := range n {
		wg.Go(func() {
			name := fmt.Sprintf("schedule-%d.yaml", i)
			if err := os.WriteFile(filepath.Join(repo, name), fmt.Appendf(nil, "id: %d\n", i), 0o644); err != nil {
				errs <- err
				return
			}
			if err := g.CommitSchedule(name, fmt.Sprintf("add %s", name)); err != nil {
				errs <- err
			}
		})
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent CommitSchedule failed: %v", err)
	}

	// Verify every commit landed — n concurrent commits should produce n new commits
	// on top of the initial "init" commit.
	out, err := runGit(repo, "log", "--oneline")
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}
	lines := splitLines(out)
	if len(lines) != n+1 {
		t.Fatalf("expected %d commits (n + init), got %d:\n%s", n+1, len(lines), out)
	}
}

func TestHasPendingPushes_NoPushes(t *testing.T) {
	repo := initTestRepo(t)

	g := NewGitOps(repo)
	pending, err := g.HasPendingPushes()
	if err != nil {
		t.Fatalf("HasPendingPushes failed: %v", err)
	}
	if pending {
		t.Fatal("expected no pending pushes for local-only repo")
	}
}
