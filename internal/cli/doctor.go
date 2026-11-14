package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leukalm/tosk/internal/config"
	"github.com/leukalm/tosk/internal/engine"
	"github.com/leukalm/tosk/internal/git"
	"github.com/leukalm/tosk/internal/hook"
	"github.com/leukalm/tosk/internal/state"
)

func newDoctor() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check hook installation, git version, orphaned allocations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			problems := 0
			ok := func(format string, a ...any) { fmt.Fprintf(out, "ok    %s\n", fmt.Sprintf(format, a...)) }
			warn := func(format string, a ...any) { fmt.Fprintf(out, "warn  %s\n", fmt.Sprintf(format, a...)) }
			fail := func(format string, a ...any) {
				problems++
				fmt.Fprintf(out, "FAIL  %s\n", fmt.Sprintf(format, a...))
			}

			v, err := git.GetVersion(ctx)
			switch {
			case err != nil:
				fail("git: %v", err)
			case !v.AtLeast(2, 20):
				// extensions.worktreeConfig, where the state lives.
				fail("git %s is too old: 2.20 or newer is required", v)
			case !v.AtLeast(2, 54):
				ok("git %s (config-based tosk-* hooks need 2.54; hook scripts work)", v)
			default:
				ok("git %s", v)
			}

			r, err := openRepo(ctx)
			if err != nil {
				fail("%v", err)
				return problemsError(problems)
			}
			main := r.Main().Path

			cfg, err := config.Load(main)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				fail("no %s in %s (run `tosk init`)", config.FileName, main)
			case err != nil:
				fail("%v", err)
			default:
				ok("%s is valid", config.FileName)
			}

			if hooks, err := git.HooksDir(ctx, main); err != nil {
				fail("%v", err)
			} else {
				setup := hook.Detect(main, hooks)
				if err := setup.Check(); err != nil {
					fail("%v", err)
				} else {
					ok("post-checkout hook registered in %s", setup.File)
				}
				if setup.Manager == hook.Husky && cfg != nil && !copiesHusky(cfg) {
					warn("with Husky, worktrees added from a linked worktree run no hook: add `- copy: .husky/_` to create")
				}
			}
			if _, err := exec.LookPath("tosk"); err != nil {
				fail("tosk is not on PATH: the hook cannot call it")
			} else {
				ok("tosk is on PATH")
			}

			if on, err := state.Enabled(ctx, main); err != nil {
				fail("%v", err)
			} else if !on {
				warn("per-worktree config is off; the first `tosk sync` turns it on")
			}

			// Allocations that should be unique, worktrees whose directory
			// is gone, worktrees not prepared yet.
			if cfg != nil {
				e, err := engine.New(cfg, r.Worktrees, engine.Options{})
				if err != nil {
					fail("%v", err)
				} else {
					duplicates := false
					for _, f := range e.Check(ctx) {
						duplicates = duplicates || f.Kind == engine.Duplicate
						if f.Severity == engine.Problem {
							fail("%s", f.Message)
						} else {
							warn("%s", f.Message)
						}
					}
					if !duplicates {
						ok("no duplicate allocation")
					}
				}
			}

			return problemsError(problems)
		},
	}
}

// problemsError is the doctor's exit status: nil when nothing failed.
func problemsError(n int) error {
	switch n {
	case 0:
		return nil
	case 1:
		return errors.New("1 problem found")
	}
	return fmt.Errorf("%d problems found", n)
}

// copiesHusky reports whether the config copies Husky's generated hooks
// into new worktrees.
func copiesHusky(cfg *config.Config) bool {
	for _, s := range cfg.Create {
		if s.Kind == config.Copy && strings.TrimSuffix(s.Arg, "/") == ".husky/_" {
			return true
		}
	}
	return false
}
