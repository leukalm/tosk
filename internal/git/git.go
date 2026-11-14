// Package git is a thin wrapper around the git binary.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// CleanEnv returns the process environment without the variables git sets
// while running a hook. They pin every git call to the hook's repository
// and worktree, which breaks `git -C <other worktree>`.
func CleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_PREFIX":
			continue
		}
		env = append(env, kv)
	}
	return env
}

// Run executes git in dir (killed when ctx is done) and returns its stdout without the final newline.
func Run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = CleanEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", &Error{Args: args, Stderr: strings.TrimSpace(stderr.String()), Err: err}
	}
	return strings.TrimRight(stdout.String(), "\r\n"), nil
}

// Error is a failed git invocation.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := e.Stderr
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

func (e *Error) Unwrap() error { return e.Err }

// ExitCode returns the exit status behind err, or -1 when git did not run
// to completion (not found, killed) or err is not a git failure. It lets
// callers tell "no such key" (git config exits 1) from a real failure.
func ExitCode(err error) int {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

// Passthrough executes git in dir with its output shown to the user.
// extraEnv entries are added to the environment.
func Passthrough(ctx context.Context, dir string, out, errOut io.Writer, extraEnv []string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(CleanEnv(), extraEnv...)
	cmd.Stdout, cmd.Stderr = out, errOut
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// Worktree is one entry of `git worktree list`.
type Worktree struct {
	Path     string
	Head     string
	Branch   string // short name, empty when detached or bare
	Bare     bool
	Detached bool
	Locked   bool
	Prunable bool
}

// Worktrees lists the repository's worktrees. The main worktree is first.
func Worktrees(ctx context.Context, dir string) ([]Worktree, error) {
	out, err := Run(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return ParseWorktrees(out), nil
}

// ParseWorktrees parses `git worktree list --porcelain`.
func ParseWorktrees(out string) []Worktree {
	var list []Worktree
	var cur *Worktree
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		key, val, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			list = append(list, Worktree{Path: filepath.FromSlash(val)})
			cur = &list[len(list)-1]
		case "":
			cur = nil
		default:
			if cur == nil {
				continue
			}
			switch key {
			case "HEAD":
				cur.Head = val
			case "branch":
				cur.Branch = strings.TrimPrefix(val, "refs/heads/")
			case "bare":
				cur.Bare = true
			case "detached":
				cur.Detached = true
			case "locked":
				cur.Locked = true
			case "prunable":
				cur.Prunable = true
			}
		}
	}
	return list
}

// Toplevel returns the root of the worktree containing dir.
func Toplevel(ctx context.Context, dir string) (string, error) {
	out, err := Run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.FromSlash(out), nil
}

func gitPath(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := Run(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	out = filepath.FromSlash(out)
	if !filepath.IsAbs(out) {
		out = filepath.Join(dir, out)
	}
	return out, nil
}

// HooksDir returns the directory git reads hooks from (honours
// core.hooksPath).
func HooksDir(ctx context.Context, dir string) (string, error) {
	return gitPath(ctx, dir, "rev-parse", "--git-path", "hooks")
}

// CommonDir returns the repository's shared .git directory.
func CommonDir(ctx context.Context, dir string) (string, error) {
	return gitPath(ctx, dir, "rev-parse", "--git-common-dir")
}

// Version is a git version.
type Version struct{ Major, Minor int }

// AtLeast reports whether v is major.minor or newer.
func (v Version) AtLeast(major, minor int) bool {
	return v.Major > major || (v.Major == major && v.Minor >= minor)
}

func (v Version) String() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }

// ParseVersion parses the output of `git version`.
func ParseVersion(out string) (Version, error) {
	fields := strings.Fields(out)
	if len(fields) < 3 {
		return Version{}, fmt.Errorf("unexpected git version output %q", out)
	}
	parts := strings.Split(fields[2], ".")
	if len(parts) < 2 {
		return Version{}, fmt.Errorf("unexpected git version %q", fields[2])
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return Version{}, fmt.Errorf("unexpected git version %q", fields[2])
	}
	return Version{major, minor}, nil
}

// GetVersion returns the version of the git binary.
func GetVersion(ctx context.Context) (Version, error) {
	out, err := Run(ctx, "", "version")
	if err != nil {
		return Version{}, err
	}
	return ParseVersion(out)
}
