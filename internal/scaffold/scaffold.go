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
