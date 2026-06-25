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
	"runtime"

	"github.com/fyang0507/sundial/skills"
)

// InstallSkillTree links the source skills/sundial/ tree into
// <dataRepo>/.agents/skills/sundial/ when running from a checkout. If the
// source tree is unavailable, it falls back to materializing the embedded tree.
func InstallSkillTree(dataRepo string) error {
	const srcRoot = "sundial"
	dest := filepath.Join(dataRepo, ".agents", "skills", "sundial")
	if source, ok := sourceSkillDir(); ok {
		if err := os.RemoveAll(dest); err != nil {
			return fmt.Errorf("removing existing %s: %w", dest, err)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(dest), err)
		}
		if err := os.Symlink(source, dest); err != nil {
			return fmt.Errorf("linking %s -> %s: %w", dest, source, err)
		}
		return nil
	}
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("removing existing %s: %w", dest, err)
	}
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

func sourceSkillDir() (string, bool) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", false
	}
	source := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "skills", "sundial"))
	if info, err := os.Stat(source); err == nil && info.IsDir() {
		return source, true
	}
	return "", false
}
