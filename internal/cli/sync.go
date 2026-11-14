package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/leukalm/tosk/internal/hook"
)

func newSync() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Reconcile every worktree with the config",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			r, err := openRepo(ctx)
			if err != nil {
				return err
			}
			e, err := r.engine(cmd, "")
			if err != nil {
				return err
			}
			return e.SyncAll(ctx)
		},
	}
}

// skipHookEnv makes the post-checkout hook a no-op. `tosk add` sets it
// because it prepares the worktree itself.
const skipHookEnv = "TOSK_SKIP_HOOK"

// newHook is what the installed git hooks call.
func newHook() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "hook",
		Short:  "Entry points for git hooks",
		Hidden: true,
	}
	cmd.AddCommand(&cobra.Command{
		Use:  "post-checkout <prev-head> <new-head> <branch-flag>",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			// git passes a null previous HEAD on `git worktree add`;
			// ordinary checkouts are none of our business.
			if !hook.IsWorktreeAdd(args) || os.Getenv(skipHookEnv) != "" {
				return nil
			}
			r, err := openRepo(ctx)
			if err != nil {
				return err
			}
			// The hook can outlive the config (branch without .tosk.yml).
			if !r.hasConfig() {
				return nil
			}
			e, err := r.engine(cmd, "")
			if err != nil {
				return err
			}
			// Only the worktree git just created: a hook must not run
			// other worktrees' steps behind the user's back.
			return e.SyncOne(ctx, r.Current)
		},
	})
	return cmd
}
