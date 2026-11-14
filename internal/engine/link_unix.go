//go:build !windows

package engine

import (
	"os"
	"path/filepath"
)

// makeLink creates name in the worktree root as a relative symlink to src
// and returns the link target.
func makeLink(root *os.Root, name, src, dst string, _ bool) (string, error) {
	target, err := filepath.Rel(filepath.Dir(dst), src)
	if err != nil {
		target = src
	}
	return target, root.Symlink(target, name)
}
