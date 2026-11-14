package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newVar() *cobra.Command {
	return &cobra.Command{
		Use:   "var [name]",
		Short: "Print one or all variables of the current worktree",
		Args:  cobra.MaximumNArgs(1),
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
			// Read-only: allocations are made by sync, not here.
			c, err := e.Inspect(ctx, r.Current)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(args) == 1 {
				val, ok := c.Vars[args[0]]
				if !ok {
					for _, p := range c.Pending {
						if p == args[0] {
							return fmt.Errorf("variable %s is not allocated yet (run `tosk sync`)", p)
						}
					}
					return fmt.Errorf("unknown variable %q", args[0])
				}
				fmt.Fprintln(out, val)
				return nil
			}
			for _, name := range c.Vars.Names() {
				fmt.Fprintf(out, "%s=%s\n", name, c.Vars[name])
			}
			return nil
		},
	}
}
