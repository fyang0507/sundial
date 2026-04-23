// Package skills holds the SKILL.md tree that `sundial setup` syncs into the
// .agents/skills/sundial/ directory of a data repo.
//
// Edit the markdown under skills/sundial/ and the updated contents are baked
// into the binary at build time via go:embed.
package skills

import "embed"

//go:embed sundial
var FS embed.FS
