package gitops

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fyang0507/sundial/internal/model"
)

// GitOps provides git operations scoped to a specific repository path.
type GitOps struct {
	repoPath string
}

// NewGitOps returns a GitOps instance bound to the given repository path.
func NewGitOps(repoPath string) *GitOps {
	return &GitOps{repoPath: repoPath}
}

// CheckRepoPreconditions verifies the repo is in a clean state suitable for
// automated commits: not detached HEAD, no rebase/merge in progress, no
// unmerged entries.
func (g *GitOps) CheckRepoPreconditions() error {
	// Not detached HEAD
	if _, err := runGit(g.repoPath, "symbolic-ref", "HEAD"); err != nil {
		return fmt.Errorf("%w: repository is in detached HEAD state", model.ErrGitPreconditionFailed)
	}

	// No rebase in progress
	gitDir := filepath.Join(g.repoPath, ".git")
	for _, dir := range []string{"rebase-merge", "rebase-apply"} {
		if info, err := os.Stat(filepath.Join(gitDir, dir)); err == nil && info.IsDir() {
			return fmt.Errorf("%w: rebase in progress", model.ErrGitPreconditionFailed)
		}
	}

	// No merge in progress
	if _, err := os.Stat(filepath.Join(gitDir, "MERGE_HEAD")); err == nil {
		return fmt.Errorf("%w: merge in progress", model.ErrGitPreconditionFailed)
	}

	// No unmerged entries
	out, err := runGit(g.repoPath, "diff", "--diff-filter=U", "--name-only")
	if err != nil {
		return fmt.Errorf("%w: failed to check unmerged entries: %v", model.ErrGitPreconditionFailed, err)
	}
	if out != "" {
		return fmt.Errorf("%w: unmerged files exist", model.ErrGitPreconditionFailed)
	}

	return nil
}

// CheckFilePreconditions verifies that the specific file has no local
// modifications (neither staged nor unstaged).
func (g *GitOps) CheckFilePreconditions(filePath string) error {
	// Check unstaged changes
	out, err := runGit(g.repoPath, "diff", "--name-only", "--", filePath)
	if err != nil {
		return fmt.Errorf("failed to check unstaged changes: %w", err)
	}
	if out != "" {
		return fmt.Errorf("file has local modifications: %s", filePath)
	}

	// Check staged changes
	out, err = runGit(g.repoPath, "diff", "--cached", "--name-only", "--", filePath)
	if err != nil {
		return fmt.Errorf("failed to check staged changes: %w", err)
	}
	if out != "" {
		return fmt.Errorf("file has local modifications: %s", filePath)
	}

	return nil
}

// CommitSchedule stages and commits a single file with the given message.
// Uses git commit --only to ensure only the target file is included in the
// commit, even if other files are staged.
func (g *GitOps) CommitSchedule(filePath, message string) error {
	if _, err := runGit(g.repoPath, "add", "--", filePath); err != nil {
		return fmt.Errorf("git add failed: %w", err)
	}
	if _, err := runGit(g.repoPath, "commit", "--only", "-m", message, "--", filePath); err != nil {
		return fmt.Errorf("git commit failed: %w", err)
	}
	return nil
}

// Push runs git push, returning any error. The caller decides retry policy.
func (g *GitOps) Push() error {
	if _, err := runGit(g.repoPath, "push"); err != nil {
		return fmt.Errorf("git push failed: %w", err)
	}
	return nil
}

// HasPendingPushes returns true if there are local commits not yet pushed to
// the upstream tracking branch. Returns false with no error if no upstream is
// configured.
func (g *GitOps) HasPendingPushes() (bool, error) {
	// Check if upstream is configured
	if _, err := runGit(g.repoPath, "rev-parse", "--abbrev-ref", "@{u}"); err != nil {
		// No upstream configured
		return false, nil
	}

	out, err := runGit(g.repoPath, "log", "@{u}..HEAD", "--oneline")
	if err != nil {
		return false, fmt.Errorf("failed to check pending pushes: %w", err)
	}

	return out != "", nil
}

// ListModifiedScheduleFiles returns a deduplicated list of files under
// schedulesDir that have either staged or unstaged modifications.
func (g *GitOps) ListModifiedScheduleFiles(schedulesDir string) ([]string, error) {
	seen := make(map[string]struct{})
	var result []string

	// Unstaged changes
	out, err := runGit(g.repoPath, "diff", "--name-only", "--", schedulesDir)
	if err != nil {
		return nil, fmt.Errorf("failed to list unstaged changes: %w", err)
	}
	for _, f := range splitLines(out) {
		if _, ok := seen[f]; !ok {
			seen[f] = struct{}{}
			result = append(result, f)
		}
	}

	// Staged changes
	out, err = runGit(g.repoPath, "diff", "--cached", "--name-only", "--", schedulesDir)
	if err != nil {
		return nil, fmt.Errorf("failed to list staged changes: %w", err)
	}
	for _, f := range splitLines(out) {
		if _, ok := seen[f]; !ok {
			seen[f] = struct{}{}
			result = append(result, f)
		}
	}

	return result, nil
}

// runGit executes a git command in the given repoPath, returning trimmed
// stdout. On failure, stderr is included in the returned error.
func runGit(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}

	return strings.TrimSpace(stdout.String()), nil
}

// splitLines splits a string by newlines, filtering out empty strings.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	result := make([]string, 0, len(lines))
	for _, l := range lines {
		if l != "" {
			result = append(result, l)
		}
	}
	return result
}
