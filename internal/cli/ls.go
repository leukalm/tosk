package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newLs() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "Worktrees with their profile, variables and status",
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
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tBRANCH\tSTATUS\tPROFILE\tVARIABLES")
			for i, wt := range r.Worktrees {
				branch := wt.Branch
				switch {
				case wt.Bare:
					branch = "(bare)"
				case wt.Detached:
					branch = "(detached)"
				}
				status, profile, variables := "", "-", ""
				switch {
				case wt.Prunable:
					status = "prunable"
				case wt.Bare:
					status = "bare"
				default:
					c, err := e.Inspect(ctx, wt)
					if err != nil {
						status = "error: " + err.Error()
						break
					}
					switch {
					case i == 0:
						status = "main"
					case c.State.Created:
						status = "ready"
					default:
						status = "pending"
					}
					if c.Profile != "" {
						profile = c.Profile
					}
					variables = strings.Join(e.UserVars(c), " ")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", filepath.Base(wt.Path), branch, status, profile, variables)
			}
			return w.Flush()
		},
	}
}
