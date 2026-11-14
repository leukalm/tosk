// Package state stores what Tosk provisioned for a worktree in that
// worktree's own git config (config.worktree), under tosk.* keys. When git
// removes or prunes the worktree, the record goes with it.
package state

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/leukalm/tosk/internal/git"
)

// State is the Tosk record of one worktree.
type State struct {
	dir string
	// id identifies the worktree; owned is set once tosk.owner holds it.
	id    string
	owned bool
	// inherited is set when the record was copied from another worktree
	// and must be wiped before anything is written.
	inherited bool

	// Created is set once the create phase has completed.
	Created bool
	// Profile is the profile the worktree was created with.
	Profile string
	// Index is the worktree's stable number; valid when HasIndex.
	Index    int
	HasIndex bool
	// Vars holds provider allocations.
	Vars map[string]string
	// Done lists the `once` run steps already executed.
	Done map[string]bool
}

// Enable turns on per-worktree config for the repository.
func Enable(ctx context.Context, mainWorktree string) error {
	on, err := Enabled(ctx, mainWorktree)
	if err != nil || on {
		return err
	}
	_, err = git.Run(ctx, mainWorktree, "config", "extensions.worktreeConfig", "true")
	return err
}

// Enabled reports whether per-worktree config is on.
func Enabled(ctx context.Context, dir string) (bool, error) {
	v, err := git.Run(ctx, dir, "config", "--get", "extensions.worktreeConfig")
	if err != nil {
		if git.ExitCode(err) == 1 { // not set
			return false, nil
		}
		return false, err
	}
	return v == "true", nil
}

// Load reads the state of the worktree at dir. A worktree without a record
// yields an empty state. A failure to read is an error, never an empty
// state: that would look like a worktree to create again.
func Load(ctx context.Context, dir string) (*State, error) {
	gitDir, linked := gitDirOf(dir)
	s := &State{dir: dir, id: "main", Vars: map[string]string{}, Done: map[string]bool{}}
	if linked {
		s.id = "worktree:" + filepath.Base(gitDir)
	}
	// No file, no record. Checked here because git treats a missing
	// config.worktree as a fatal error, which must not be confused with a
	// failure to read an existing one.
	if _, err := os.Stat(filepath.Join(gitDir, "config.worktree")); errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	out, err := git.Run(ctx, dir, "config", "--worktree", "--list", "-z")
	if err != nil {
		return nil, fmt.Errorf("reading the tosk record of %s: %w", dir, err)
	}
	entries := strings.Split(out, "\x00")
	// `git worktree add` copies the config.worktree of the worktree it is
	// run from into the new one. A record owned by another worktree is
	// such a copy: its allocations are not ours.
	hasRecord := false
	for _, entry := range entries {
		key, val, _ := strings.Cut(entry, "\n")
		if key == "tosk.owner" {
			s.owned = val == s.id
		}
		hasRecord = hasRecord || strings.HasPrefix(key, "tosk.")
	}
	if !s.owned {
		s.inherited = hasRecord
		return s, nil
	}
	for _, entry := range entries {
		key, val, _ := strings.Cut(entry, "\n")
		switch {
		case key == "tosk.created":
			s.Created = val == "true"
		case key == "tosk.profile":
			s.Profile = val
		case key == "tosk.index":
			if n, err := strconv.Atoi(val); err == nil {
				s.Index, s.HasIndex = n, true
			}
		case key == "tosk.done":
			s.Done[val] = true
		case strings.HasPrefix(key, "tosk.var."):
			s.Vars[strings.ReplaceAll(strings.TrimPrefix(key, "tosk.var."), "-", "_")] = val
		}
	}
	return s, nil
}

// gitDirOf returns the git directory of the worktree at dir, and whether
// it is a linked worktree: there, .git is a file pointing to
// <repo>/.git/worktrees/<id>. That id names the worktree independently of
// its path, so the record survives `git worktree move`.
func gitDirOf(dir string) (gitDir string, linked bool) {
	dotGit := filepath.Join(dir, ".git")
	data, err := os.ReadFile(dotGit)
	if err != nil {
		return dotGit, false // .git is a directory
	}
	gitDir = filepath.FromSlash(strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir:")))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	return gitDir, true
}

// own makes the record ours before the first write, wiping one inherited
// from another worktree.
func (s *State) own(ctx context.Context) error {
	if s.owned {
		return nil
	}
	if s.inherited {
		// Errors only mean the section is already absent.
		_, _ = git.Run(ctx, s.dir, "config", "--worktree", "--remove-section", "tosk.var")
		_, _ = git.Run(ctx, s.dir, "config", "--worktree", "--remove-section", "tosk")
		s.inherited = false
	}
	if _, err := git.Run(ctx, s.dir, "config", "--worktree", "tosk.owner", s.id); err != nil {
		return err
	}
	s.owned = true
	return nil
}

func (s *State) set(ctx context.Context, key, val string) error {
	if err := s.own(ctx); err != nil {
		return err
	}
	_, err := git.Run(ctx, s.dir, "config", "--worktree", key, val)
	return err
}

// SetCreated records that the create phase completed.
func (s *State) SetCreated(ctx context.Context) error {
	s.Created = true
	return s.set(ctx, "tosk.created", "true")
}

// SetProfile records the worktree's profile.
func (s *State) SetProfile(ctx context.Context, p string) error {
	s.Profile = p
	return s.set(ctx, "tosk.profile", p)
}

// SetIndex records the worktree's index.
func (s *State) SetIndex(ctx context.Context, n int) error {
	s.Index, s.HasIndex = n, true
	return s.set(ctx, "tosk.index", strconv.Itoa(n))
}

// SetVar records a provider allocation. Git config keys cannot contain
// underscores, so they are stored as dashes.
func (s *State) SetVar(ctx context.Context, name, val string) error {
	s.Vars[name] = val
	return s.set(ctx, "tosk.var."+strings.ReplaceAll(name, "_", "-"), val)
}

// MarkDone records that a `once` step ran.
func (s *State) MarkDone(ctx context.Context, id string) error {
	if s.Done[id] {
		return nil
	}
	if err := s.own(ctx); err != nil {
		return err
	}
	s.Done[id] = true
	_, err := git.Run(ctx, s.dir, "config", "--worktree", "--add", "tosk.done", id)
	return err
}
