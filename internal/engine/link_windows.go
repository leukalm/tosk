//go:build windows

package engine

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// makeLink creates name in the worktree root as a link to src. Symlinks
// need a privilege most Windows accounts lack, so directories fall back to
// a junction. The caller has created the parent directories through the
// root, so dst is inside the worktree.
func makeLink(root *os.Root, name, src, dst string, isDir bool) (string, error) {
	err := root.Symlink(src, name)
	if err == nil || !isDir {
		return src, err
	}
	out, jerr := exec.Command("cmd", "/C", "mklink", "/J", dst, src).CombinedOutput()
	if jerr != nil {
		return "", fmt.Errorf("mklink /J: %s", strings.TrimSpace(string(out)))
	}
	return src, nil
}
