package cli

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/leukalm/tosk/internal/git"
)

func newRm() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "rm <path>",
		Short: "remove phase, release providers, then git worktree remove",
		Long: `Tear a worktree down and remove it.

The worktree can be given by path, directory name or branch. A worktree
with uncommitted changes, or locked (as Claude Code does with the ones it
creates), is refused before anything runs. With --force, failing remove steps
are ignored and git removes the worktree anyway.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			r, err := openRepo(ctx)
			if err != nil {
				return err
			}
			wt, err := r.find(args[0])
			if err != nil {
				return err
			}
			if wt.Path == r.Main().Path {
				return errors.New("refusing to remove the main worktree")
			}
			// Find out now whether git will agree to remove the worktree:
			// the remove phase drops databases and the like, and must not
			// run for a worktree that then stays.
			if !force && !wt.Prunable {
				if wt.Locked {
					return fmt.Errorf("%s is locked (`git worktree unlock`, or --force)", filepath.Base(wt.Path))
				}
				dirty, err := git.Run(ctx, wt.Path, "status", "--porcelain")
				if err != nil {
					return err
				}
				if dirty != "" {
					return fmt.Errorf("%s has modified or untracked files; nothing was removed (commit them, or --force):\n%s",
						filepath.Base(wt.Path), dirty)
				}
			}
			if !wt.Prunable {
				e, err := r.engine(cmd, "")
				if err != nil {
					return err
				}
				if err := e.Remove(ctx, wt, force); err != nil {
					return err
				}
			}
			gitArgs := []string{"worktree", "remove"}
			if force {
				gitArgs = append(gitArgs, "--force")
				// git wants the flag twice to remove a locked worktree
				// (Claude Code locks the ones it creates). Without it the
				// remove phase would have run for a worktree that stays.
				if wt.Locked {
					gitArgs = append(gitArgs, "--force")
				}
			}
			gitArgs = append(gitArgs, wt.Path)
			return git.Passthrough(ctx, r.Main().Path, cmd.OutOrStdout(), cmd.ErrOrStderr(), nil, gitArgs...)
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "ignore failing steps and uncommitted changes")
	return cmd
}
