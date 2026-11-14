package cli

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leukalm/tosk/internal/git"
	"github.com/leukalm/tosk/internal/vars"
)

func newAdd() *cobra.Command {
	var profile string
	cmd := &cobra.Command{
		Use:   "add <branch> [path]",
		Short: "git worktree add, then the create phase",
		Long: `Create a worktree for a branch and prepare it.

The branch is created if it does not exist. Without a path, the worktree is
placed next to the main one: app + feat/invoices gives ../app-feat-invoices.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			r, err := openRepo(ctx)
			if err != nil {
				return err
			}
			if _, err := r.loadConfig(); err != nil {
				return err
			}
			main := r.Main().Path
			branch := args[0]

			path := filepath.Join(filepath.Dir(main),
				filepath.Base(main)+"-"+strings.ReplaceAll(vars.Slug(branch), "_", "-"))
			if len(args) == 2 {
				if path, err = filepath.Abs(args[1]); err != nil {
					return err
				}
			}

			gitArgs := []string{"worktree", "add"}
			_, err = git.Run(ctx, main, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
			switch {
			case err == nil:
				gitArgs = append(gitArgs, path, branch)
			case git.ExitCode(err) != 1: // 1 is "no such branch"
				return err
			default:
				if remote := remoteBranch(ctx, main, branch); remote != "" {
					gitArgs = append(gitArgs, "--track", "-b", branch, path, remote)
				} else {
					gitArgs = append(gitArgs, "-b", branch, path)
				}
			}

			// Keep the post-checkout hook quiet: this command prepares
			// the new worktree itself, with the requested profile.
			env := []string{skipHookEnv + "=1"}
			if err := git.Passthrough(ctx, main, cmd.OutOrStdout(), cmd.ErrOrStderr(), env, gitArgs...); err != nil {
				return err
			}

			r, err = openRepo(ctx)
			if err != nil {
				return err
			}
			wt, err := r.find(path)
			if err != nil {
				return err
			}
			e, err := r.engine(cmd, profile)
			if err != nil {
				return err
			}
			return e.SyncOne(ctx, wt)
		},
	}
	cmd.Flags().StringVar(&profile, "profile", "", "profile to prepare the worktree with")
	return cmd
}

// remoteBranch returns "<remote>/<branch>" when exactly one remote has the
// branch.
func remoteBranch(ctx context.Context, dir, branch string) string {
	out, err := git.Run(ctx, dir, "for-each-ref", "--format=%(refname:short)", "refs/remotes/*/"+branch)
	if err != nil || out == "" || strings.Contains(out, "\n") {
		return ""
	}
	return out
}
