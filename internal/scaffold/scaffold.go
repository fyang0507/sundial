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
#   socket_path: "~/Library/Application Support/sundial/sundial.sock"
#   log_level: info                      # debug | info | warn | error
#   log_file: "~/Library/Logs/sundial/sundial.log"
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
