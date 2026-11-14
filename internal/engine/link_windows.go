//go:build windows

package engine

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// makeLink creates name in the worktree root as a link to src.
//
// Windows symlinks are typed: a link to a directory must be created as a
// directory link, which os.Symlink decides by looking at the target. Root's
// Symlink does not look at a target outside the root and makes a file link,
// which cannot be traversed. So directories go through os.Symlink on the
// full path; the caller has created the parent directories through the
// root, so dst is inside the worktree. Symlinks need a privilege many
// Windows accounts lack, so directories fall back to a junction.
func makeLink(root *os.Root, name, src, dst string, isDir bool) (string, error) {
	if !isDir {
		return src, root.Symlink(src, name)
	}
	if err := os.Symlink(src, dst); err == nil {
		return src, nil
	}
	out, err := exec.Command("cmd", "/C", "mklink", "/J", dst, src).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("mklink /J: %s", strings.TrimSpace(string(out)))
	}
	return src, nil
}
