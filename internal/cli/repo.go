package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/leukalm/tosk/internal/config"
	"github.com/leukalm/tosk/internal/engine"
	"github.com/leukalm/tosk/internal/git"
)

// repo is the repository seen from the current directory.
type repo struct {
	Worktrees []git.Worktree
	Current   git.Worktree
}

// Main is the main worktree, where .tosk.yml lives.
func (r *repo) Main() git.Worktree { return r.Worktrees[0] }

func openRepo(ctx context.Context) (*repo, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	top, err := git.Toplevel(ctx, cwd)
	if err != nil {
		return nil, errors.New("not inside a git worktree")
	}
	list, err := git.Worktrees(ctx, top)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, errors.New("git reported no worktree")
	}
	r := &repo{Worktrees: list}
	for _, wt := range list {
		if samePath(wt.Path, top) {
			r.Current = wt
			return r, nil
		}
	}
	return nil, fmt.Errorf("%s is not a registered worktree", top)
}

func samePath(a, b string) bool {
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return os.SameFile(ai, bi)
}

// find returns the worktree designated by arg: a path, a directory name or
// a branch.
func (r *repo) find(arg string) (git.Worktree, error) {
	abs, _ := filepath.Abs(arg)
	for _, wt := range r.Worktrees {
		if samePath(wt.Path, abs) {
			return wt, nil
		}
	}
	for _, wt := range r.Worktrees {
		if filepath.Base(wt.Path) == arg || (wt.Branch != "" && wt.Branch == arg) {
			return wt, nil
		}
	}
	return git.Worktree{}, fmt.Errorf("no worktree matches %q", arg)
}

func (r *repo) hasConfig() bool {
	_, err := os.Stat(filepath.Join(r.Main().Path, config.FileName))
	return err == nil
}

// loadConfig reads .tosk.yml from the main worktree.
func (r *repo) loadConfig() (*config.Config, error) {
	c, err := config.Load(r.Main().Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("no %s in %s (run `tosk init`)", config.FileName, r.Main().Path)
	}
	return c, err
}

// profileEnv names the environment variable that selects the profile of new
// worktrees when no --profile flag is given.
const profileEnv = "TOSK_PROFILE"

// engine builds the engine for the repository. profile is the --profile
// flag; when empty, TOSK_PROFILE applies.
func (r *repo) engine(cmd *cobra.Command, profile string) (*engine.Engine, error) {
	cfg, err := r.loadConfig()
	if err != nil {
		return nil, err
	}
	if profile == "" {
		profile = os.Getenv(profileEnv)
	}
	return engine.New(cfg, r.Worktrees, engine.Options{
		Out:     cmd.OutOrStdout(),
		Err:     cmd.ErrOrStderr(),
		Profile: profile,
	})
}
