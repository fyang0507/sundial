package gitops

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fyang0507/sundial/internal/model"
)

// Git invocations are bounded so a hung operation can never wedge the daemon's
// single-writer mutating path indefinitely (issue #43). The daemon runs under
// launchd with no controlling terminal: an SSH push that blocks on a passphrase
// or host-key prompt, or an HTTPS push waiting on credentials, would otherwise
// hang forever — and because Push and CommitSchedule share g.mu, that hang
// wedges every subsequent mutating RPC while health/list stay responsive.
//
// localGitTimeout bounds fast local operations; networkGitTimeout bounds push.
// The push budget stays under the CLI's 30s RPC deadline so `add` still returns
// a result (with a push warning) instead of leaving the caller with a timeout.
const (
	localGitTimeout   = 15 * time.Second
	networkGitTimeout = 25 * time.Second
)

// GitOps provides git operations scoped to a specific repository path.
//
// Mutating operations (CommitSchedule, Push) are serialized via mu so that
// concurrent writers — e.g. the scheduler goroutine completing a schedule and
// an RPC handler refreshing the same schedule — cannot race on .git/index.lock
// or ref locks. The mutex is held only for the duration of each method, not
// across commit+push pairs, so a slow push does not block an unrelated commit.
type GitOps struct {
	repoPath string
	mu       sync.Mutex
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
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, err := runGit(g.repoPath, "add", "--", filePath); err != nil {
		return fmt.Errorf("git add failed: %w", err)
	}
	if _, err := runGit(g.repoPath, "commit", "--only", "-m", message, "--", filePath); err != nil {
		return fmt.Errorf("git commit failed: %w", err)
	}
	return nil
}

// Push runs git push, returning any error. The caller decides retry policy.
// Push is the one network operation here, so it gets networkGitTimeout and
// (via runGitTimeout) runs with interactive prompts disabled — a push that
// cannot authenticate fails fast instead of blocking the daemon forever.
func (g *GitOps) Push() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, err := runGitTimeout(g.repoPath, networkGitTimeout, "push"); err != nil {
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

// runGit executes a local git command in the given repoPath, returning trimmed
// stdout. On failure, stderr is included in the returned error. It uses
// localGitTimeout — callers that touch the network (push) use runGitTimeout
// with a larger budget.
func runGit(repoPath string, args ...string) (string, error) {
	return runGitTimeout(repoPath, localGitTimeout, args...)
}

// runGitTimeout runs a git command bounded by the given timeout and with
// interactive prompts disabled (see gitEnv). A timeout surfaces as a clear
// error rather than a hang. On failure, stderr is included in the error.
func runGitTimeout(repoPath string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoPath
	cmd.Env = gitEnv()

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("git %s: timed out after %s (the daemon runs non-interactively; a credential or host-key prompt may be blocking)", strings.Join(args, " "), timeout)
	}
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}

	return strings.TrimSpace(stdout.String()), nil
}

// gitEnv returns the daemon's environment with interactive prompts disabled.
// Without a controlling terminal, any git operation that would prompt for input
// blocks forever. GIT_TERMINAL_PROMPT=0 suppresses git's own prompts (HTTPS
// credentials); forcing ssh into BatchMode suppresses SSH passphrase and
// host-key prompts. Both are set only when the operator hasn't already provided
// a value, so an explicit GIT_SSH_COMMAND (e.g. a custom key) still wins.
func gitEnv() []string {
	env := os.Environ()
	if os.Getenv("GIT_TERMINAL_PROMPT") == "" {
		env = append(env, "GIT_TERMINAL_PROMPT=0")
	}
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		env = append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=10")
	}
	return env
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
