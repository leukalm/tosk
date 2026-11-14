// Package skills embeds the Agent Skills shipped with Tosk, so that
// `tosk skill` writes the version matching the binary. The same files are
// what the Claude Code plugin in plugin/ serves.
package skills

import _ "embed"

// Tosk is plugin/skills/tosk/SKILL.md.
//
//go:embed tosk/SKILL.md
var Tosk string
