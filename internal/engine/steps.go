package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/leukalm/tosk/internal/config"
	"github.com/leukalm/tosk/internal/expr"
	"github.com/leukalm/tosk/internal/git"
)

// roots confine file access. A worktree holds whatever its branch
// committed, including symlinks pointing anywhere, and Tosk runs
// unattended from a git hook: every read and write of a step goes through
// an os.Root, which refuses to leave its directory, through a symlink or
// otherwise.
type roots struct {
	wt   *os.Root // the worktree being prepared
	main *os.Root // the main worktree, source of copies
}

func (e *Engine) openRoots(c *Target) (roots, func(), error) {
	wt, err := os.OpenRoot(c.WT.Path)
	if err != nil {
		return roots{}, nil, err
	}
	main, err := os.OpenRoot(e.main.Path)
	if err != nil {
		wt.Close()
		return roots{}, nil, err
	}
	return roots{wt, main}, func() { wt.Close(); main.Close() }, nil
}

// runStep executes one step. Steps report only what they changed, so a
// sync with nothing to do prints nothing. With force, `when` and the
// `once` record are ignored.
func (e *Engine) runStep(ctx context.Context, c *Target, phase string, s config.Step, force bool) error {
	if !force && s.When != "" {
		ok, err := expr.Eval(s.When, c.exprEnv())
		if err != nil || !ok {
			return err
		}
	}
	if s.Kind == config.Run {
		return e.stepRun(ctx, c, phase, s, force)
	}
	r, closeRoots, err := e.openRoots(c)
	if err != nil {
		return err
	}
	defer closeRoots()
	switch s.Kind {
	case config.Copy:
		return e.stepCopy(c, r, s)
	case config.Link:
		return e.stepLink(c, r, s)
	case config.Template:
		return e.stepTemplate(c, r, s)
	case config.Replace:
		return e.stepReplace(c, r, s)
	case config.Write:
		return e.stepWrite(c, r, s)
	}
	return nil
}

// local normalises a path from the config to a slash-separated path
// relative to a root. os.Root enforces the confinement; this check gives a
// clear message for the obvious cases (.., absolute, drive-relative).
func local(p string) (string, error) {
	clean := path.Clean(filepath.ToSlash(p))
	if !filepath.IsLocal(filepath.FromSlash(clean)) {
		return "", fmt.Errorf("%s: path must be relative and stay inside the worktree", p)
	}
	return clean, nil
}

// mkParents creates the directories leading to name.
func mkParents(root *os.Root, name string) error {
	if dir := path.Dir(name); dir != "." {
		return root.MkdirAll(dir, 0o755)
	}
	return nil
}

// isLinkMode reports whether a file is a symlink or a Windows junction.
func isLinkMode(info fs.FileInfo) bool {
	return info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0
}

func (e *Engine) stepCopy(c *Target, r roots, s config.Step) error {
	expanded, err := c.Vars.Expand(s.Arg)
	if err != nil {
		return err
	}
	pattern, err := local(expanded)
	if err != nil {
		return err
	}
	matches, err := fs.Glob(r.main.FS(), pattern)
	if err != nil {
		return err
	}
	n := 0
	for _, m := range matches {
		copied, err := copyTree(r, m)
		if err != nil {
			return err
		}
		n += copied
	}
	if n == 1 {
		c.say("copy", "%s (1 file)", pattern)
	} else if n > 1 {
		c.say("copy", "%s (%d files)", pattern, n)
	}
	return nil
}

// copyTree copies a file or directory from the main worktree to the same
// place in the worktree, and returns the number of files copied. Anything
// that already exists at the destination is left alone. Symlinks are
// recreated, not followed.
func copyTree(r roots, name string) (int, error) {
	info, err := r.main.Lstat(name)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return copyEntry(r, name, info)
	}
	n := 0
	err = fs.WalkDir(r.main.FS(), name, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return r.wt.MkdirAll(p, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		copied, err := copyEntry(r, p, info)
		n += copied
		return err
	})
	return n, err
}

func copyEntry(r roots, name string, info fs.FileInfo) (int, error) {
	if _, err := r.wt.Lstat(name); err == nil {
		return 0, nil
	}
	if err := mkParents(r.wt, name); err != nil {
		return 0, err
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := r.main.Readlink(name)
		if err != nil {
			return 0, err
		}
		return 1, r.wt.Symlink(target, name)
	case !info.Mode().IsRegular():
		return 0, nil
	}
	in, err := r.main.Open(name)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	// O_EXCL: never write through something that appeared meanwhile. The
	// mode is kept: copied files are typically secrets.
	out, err := r.wt.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return 0, err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return 0, err
	}
	return 1, out.Close()
}

func (e *Engine) stepLink(c *Target, r roots, s config.Step) error {
	expanded, err := c.Vars.Expand(s.Arg)
	if err != nil {
		return err
	}
	name, err := local(expanded)
	if err != nil {
		return err
	}
	src, err := r.main.Stat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil // nothing to share yet in the main worktree
	}
	if err != nil {
		return err
	}
	if dst, err := r.wt.Lstat(name); err == nil {
		if isLinkMode(dst) {
			return nil
		}
		return fmt.Errorf("%s already exists and is not a link", name)
	}
	if err := mkParents(r.wt, name); err != nil {
		return err
	}
	osName := filepath.FromSlash(name)
	target, err := makeLink(r.wt, name, filepath.Join(e.main.Path, osName), filepath.Join(c.WT.Path, osName), src.IsDir())
	if err != nil {
		return err
	}
	c.say("link", "%s -> %s", name, target)
	return nil
}

func (e *Engine) stepTemplate(c *Target, r roots, s config.Step) error {
	expanded, err := c.Vars.Expand(s.Arg)
	if err != nil {
		return err
	}
	name, err := local(expanded)
	if err != nil {
		return err
	}
	if !strings.HasSuffix(name, ".tpl") {
		return fmt.Errorf("%s: template files end in .tpl", name)
	}
	src := r.wt
	if _, err := src.Lstat(name); errors.Is(err, fs.ErrNotExist) {
		src = r.main // an untracked template only exists in the main worktree
	}
	data, err := src.ReadFile(name)
	if err != nil {
		return err
	}
	perm := fs.FileMode(0o644)
	if info, err := src.Stat(name); err == nil {
		perm = info.Mode().Perm()
	}
	rendered, err := c.Vars.Expand(string(data))
	if err != nil {
		return err
	}
	out := strings.TrimSuffix(name, ".tpl")
	changed, err := writeIfChanged(r.wt, out, []byte(rendered), perm)
	if changed {
		c.say("tmpl", "%s", out)
	}
	return err
}

func (e *Engine) stepReplace(c *Target, r roots, s config.Step) error {
	expanded, err := c.Vars.Expand(s.File)
	if err != nil {
		return err
	}
	name, err := local(expanded)
	if err != nil {
		return err
	}
	// Variable values are data: they must match literally in the pattern
	// and be inserted literally by the replacement, whatever characters a
	// branch name contains.
	match, err := c.Vars.ExpandQuoted(s.Match, regexp.QuoteMeta)
	if err != nil {
		return err
	}
	with, err := c.Vars.ExpandQuoted(s.With, func(v string) string { return strings.ReplaceAll(v, "$", "$$") })
	if err != nil {
		return err
	}
	// Multi-line mode, so that ^ and $ work on each line of the file.
	re, err := regexp.Compile("(?m)" + match)
	if err != nil {
		return err
	}
	data, err := r.wt.ReadFile(name)
	if err != nil {
		return err
	}
	changed, err := writeIfChanged(r.wt, name, re.ReplaceAll(data, []byte(with)), 0)
	if changed {
		c.say("repl", "%s", name)
	}
	return err
}

func (e *Engine) stepWrite(c *Target, r roots, s config.Step) error {
	expanded, err := c.Vars.Expand(s.File)
	if err != nil {
		return err
	}
	name, err := local(expanded)
	if err != nil {
		return err
	}
	content, err := c.Vars.Expand(s.Content)
	if err != nil {
		return err
	}
	changed, err := writeIfChanged(r.wt, name, []byte(content), 0)
	if changed {
		c.say("write", "%s", name)
	}
	return err
}

// writeIfChanged writes data to name unless the file already holds it. An
// existing file keeps its mode; a new one gets perm, or 0644 when perm is
// zero.
func writeIfChanged(root *os.Root, name string, data []byte, perm fs.FileMode) (bool, error) {
	old, err := root.ReadFile(name)
	if err == nil && bytes.Equal(old, data) {
		return false, nil
	}
	if perm == 0 {
		perm = 0o644
	}
	if info, err := root.Stat(name); err == nil {
		perm = info.Mode().Perm()
	}
	if err := mkParents(root, name); err != nil {
		return false, err
	}
	if err := root.WriteFile(name, data, perm); err != nil {
		return false, err
	}
	return true, nil
}

func (e *Engine) stepRun(ctx context.Context, c *Target, phase string, s config.Step, force bool) error {
	id := phase + ":" + s.Name()
	once := s.Mode != config.ModeAlways && phase != "remove"
	if once && !force && c.State.Done[id] {
		return nil
	}
	// What is shown has the values in; what is executed refers to them
	// through the environment (see vars.ShellExpand).
	display, err := c.Vars.Expand(s.Arg)
	if err != nil {
		return err
	}
	script, err := c.Vars.ShellExpand(s.Arg)
	if err != nil {
		return err
	}
	cmd, err := shell(ctx, script, c)
	if err != nil {
		return err
	}
	// The command's output is kept quiet unless it fails.
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	start := time.Now()
	err = cmd.Run()
	took := ""
	if d := time.Since(start); d >= time.Second {
		took = fmt.Sprintf(" (%s)", d.Round(time.Second))
	}
	c.say("run", "%s%s", display, took)
	if err != nil {
		_, _ = e.err.Write(output.Bytes())
		return err
	}
	if once {
		return c.State.MarkDone(ctx, id)
	}
	return nil
}

// waitDelay bounds how long a cancelled command may keep its pipes open.
const waitDelay = 5 * time.Second

// shell builds a command run by sh in the worktree, with the variables in
// its environment as TOSK_<NAME>.
func shell(ctx context.Context, script string, c *Target) (*exec.Cmd, error) {
	sh, err := shPath(ctx)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, sh, "-c", script)
	cmd.Dir = c.WT.Path
	cmd.Env = append(git.CleanEnv(), c.Vars.Env()...)
	// After cancellation, do not wait forever for output held open by
	// grandchildren.
	cmd.WaitDelay = waitDelay
	return cmd, nil
}

// shPath locates a POSIX shell. Commands in .tosk.yml are sh command
// lines on every platform; on Windows that is the sh shipped with Git for
// Windows, on PATH in Git Bash and next to git otherwise.
func shPath(ctx context.Context) (string, error) {
	if runtime.GOOS != "windows" {
		return "sh", nil
	}
	if sh, err := exec.LookPath("sh"); err == nil {
		return sh, nil
	}
	if execPath, err := git.Run(ctx, "", "--exec-path"); err == nil {
		// <root>/mingw64/libexec/git-core
		root := filepath.Join(filepath.FromSlash(execPath), "..", "..", "..")
		for _, rel := range []string{"bin/sh.exe", "usr/bin/sh.exe"} {
			sh := filepath.Join(root, filepath.FromSlash(rel))
			if _, err := os.Stat(sh); err == nil {
				return sh, nil
			}
		}
	}
	return "", errors.New("no sh found: run steps need the shell shipped with Git for Windows")
}

// event runs the hooks registered for a Tosk event, through `git hook
// run`: scripts in the hooks directory, and config-based hooks on git
// versions that support them.
func (e *Engine) event(ctx context.Context, c *Target, name string) error {
	v, err := git.GetVersion(ctx)
	if err != nil || !v.AtLeast(2, 36) {
		return nil // `git hook run` does not exist
	}
	cmd := exec.CommandContext(ctx, "git", "hook", "run", "--ignore-missing", name)
	cmd.Dir = c.WT.Path
	cmd.Env = append(git.CleanEnv(), c.Vars.Env()...)
	cmd.Stdout, cmd.Stderr = e.out, e.err
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s hook: %w", name, err)
	}
	return nil
}
