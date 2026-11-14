// Package hook installs Tosk on a repository's post-checkout hook.
package hook

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	begin = "# >>> tosk >>>"
	end   = "# <<< tosk <<<"
)

// block returns the lines added to post-checkout.
//
// The hook runs inside the new worktree, whose content comes from the
// branch: finding tosk through PATH alone would let a relative PATH entry
// (".", "bin") run a tosk committed on that branch. So the binary that
// ran `tosk init` is called by absolute path, and PATH is only the
// fallback for when it has moved. Without tosk at all the hook is a no-op,
// so a clone without Tosk installed keeps working.
func block(exe string) string {
	call := ` hook post-checkout "$@"`
	var b strings.Builder
	b.WriteString(begin + "\n")
	if exe != "" {
		q := shellQuote(exe)
		b.WriteString("if [ -x " + q + " ]; then\n  " + q + call + "\nelif")
	} else {
		b.WriteString("if")
	}
	b.WriteString(" command -v tosk >/dev/null 2>&1; then\n  tosk" + call + "\nfi\n" + end + "\n")
	return b.String()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Result says what Install did.
type Result int

const (
	Created Result = iota // new hook script
	Chained               // appended after an existing script
	Updated               // the Tosk block was there and has been rewritten
	Present               // already installed and current
)

// Install registers Tosk in hooksDir/post-checkout, calling the tosk
// binary at exe (an absolute path; empty to rely on PATH). An existing
// script is kept and Tosk runs after it.
//
// TODO: hook managers that regenerate their scripts (Husky's .husky/_,
// Lefthook) need their own integration instead of an appended block.
func Install(hooksDir, exe string) (Result, error) {
	path := filepath.Join(hooksDir, "post-checkout")
	want := block(exe)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(hooksDir, 0o755); err != nil {
			return 0, err
		}
		return Created, os.WriteFile(path, []byte("#!/bin/sh\n"+want), 0o755)
	}
	if err != nil {
		return 0, err
	}
	content := string(data)
	result := Chained
	if i, j := strings.Index(content, begin), strings.Index(content, end); i >= 0 && j > i {
		j += len(end)
		if j < len(content) && content[j] == '\n' {
			j++
		}
		if content[i:j] == want {
			return Present, nil
		}
		content = content[:i] + want + content[j:]
		result = Updated
	} else {
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "\n" + want
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		return 0, err
	}
	return result, os.Chmod(path, 0o755)
}

// Installed reports whether a hook script already contains the Tosk block.
func Installed(script string) bool {
	return strings.Contains(script, begin)
}

// IsWorktreeAdd reports whether post-checkout arguments describe a fresh
// worktree: git passes a null previous HEAD on `git worktree add`.
func IsWorktreeAdd(args []string) bool {
	return len(args) >= 1 && args[0] != "" && strings.Trim(args[0], "0") == ""
}
