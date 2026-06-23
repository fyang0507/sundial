// Package scaffold holds the templates and orchestration written by
// `sundial setup` when it bootstraps a data repo. The SKILL.md tree itself
// lives at the repo root under skills/sundial/ and is embedded via the
// top-level skills package.
package scaffold

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/fyang0507/sundial/skills"
)

// ConfigTemplate is the content written to <data_repo>/sundial/config.yaml
// the first time `sundial setup` scaffolds a data repo. Daemon defaults apply
// when fields are omitted; everything here is commented out.
const ConfigTemplate = `# Daemon options for sundial. The data_repo itself is resolved via
# SUNDIAL_DATA_REPO, sundial.config.dev.yaml, or .agents/workspace.yaml —
# it is not a field in this file.
#
# All fields below have sensible defaults. Uncomment to override.
#
# daemon:
#   log_level: info                      # debug | info | warn | error
#   log_file: "~/Library/Logs/sundial/sundial.log"
#   # Default backoff schedule for retrying a deferred --precondition (Go
#   # durations). The last entry repeats as the cap. Per-schedule
#   # --precondition-backoff overrides this for a single schedule.
#   precondition_backoff: ["1m", "5m", "30m", "1h", "2h"]
#   # Give-up budget for a precondition on a one-off 'at' schedule (no next
#   # regular fire to bound retries). Defaults to the backoff cap.
#   precondition_max_elapsed: "2h"
#   # Window within which a fire missed while the daemon was offline/asleep is
#   # still executed once on the next tick (beyond it: logged as a miss).
#   miss_grace_period: "60s"
#   # Wake a sleeping Mac before a due schedule (macOS pmset). Off by default.
#   # When enabled, the daemon schedules ONE pmset wake event for the soonest
#   # fire across all active schedules, 'lead_time' before it. Enabling requires
#   # a passwordless sudoers rule for pmset — run 'sundial health' (or see
#   # setup.md) for the exact line to install.
#   wake:
#     enabled: false
#     lead_time: "3m"
#
# state:
#   path: "~/.config/sundial/state/"     # runtime state (daemon-managed, not portable)
#   logs_path: "~/.config/sundial/logs/" # run logs (local only)
`

// CopySkills copies the embedded skills/sundial/ tree into
// <dataRepo>/.agents/skills/sundial/. Existing files are overwritten so the
// command is idempotent across upgrades.
func CopySkills(dataRepo string) error {
	const srcRoot = "sundial"
	dest := filepath.Join(dataRepo, ".agents", "skills", "sundial")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dest, err)
	}
	return fs.WalkDir(skills.FS, srcRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := p[len(srcRoot):]
		rel = filepath.FromSlash(rel)
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := skills.FS.ReadFile(p)
		if err != nil {
			return fmt.Errorf("reading embedded %s: %w", p, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", target, err)
		}
		return nil
	})
}
