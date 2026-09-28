// Package skills embeds the agent skill so `radaro skill install` can put it
// into Claude Code, Codex and other Agent Skills-compatible tools.
package skills

import (
	"embed"
	"io/fs"
)

//go:embed radaro
var files embed.FS

// Radaro returns the radaro skill directory (SKILL.md + references/).
func Radaro() fs.FS {
	sub, err := fs.Sub(files, "radaro")
	if err != nil {
		panic(err)
	}
	return sub
}
