package cli

import (
	"github.com/spf13/cobra"
)

func newRun() *cobra.Command {
	return &cobra.Command{
		Use:   "run <step>",
		Short: "Re-run a single step by name",
		Long: `Run one step in the current worktree, ignoring its "when" condition and
whether it already ran.

A step is named <type>:<argument> as written in .tosk.yml, for example
"link:node_modules" or "run:./scripts/create-db.sh {{db}}".`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			r, err := openRepo(ctx)
			if err != nil {
				return err
			}
			e, err := r.engine(cmd, "")
			if err != nil {
				return err
			}
			return e.RunStep(ctx, r.Current, args[0])
		},
	}
}

func newWatch() *cobra.Command {
	return &cobra.Command{
		Use:   "watch",
		Short: "Sync on git worktree events (not implemented yet)",
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return errNotImplemented },
	}
}
