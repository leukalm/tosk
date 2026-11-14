package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leukalm/tosk/internal/config"
	"github.com/leukalm/tosk/internal/git"
	"github.com/leukalm/tosk/internal/hook"
)

const starterConfig = `# Tosk: environment preparation for git worktrees.
# https://github.com/leukalm/tosk

vars: {}
  # port: { provider: port, base: 8100, range: 100 }
  # db:   "myapp_{{slug}}"

create: []
  # - copy: .env
  # - link: node_modules
  # - run: ./scripts/create-db.sh {{db}}

sync: []

# remove defaults to the reverse of create.
`

func newInit() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create .tosk.yml and install the hook",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			r, err := openRepo(ctx)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			main := r.Main().Path

			cfg := filepath.Join(main, config.FileName)
			if _, err := os.Stat(cfg); errors.Is(err, fs.ErrNotExist) {
				if err := os.WriteFile(cfg, []byte(starterConfig), 0o644); err != nil {
					return err
				}
				fmt.Fprintf(out, "tosk: wrote %s\n", cfg)
			} else if err != nil {
				return err
			} else {
				fmt.Fprintf(out, "tosk: %s already exists, left as is\n", cfg)
			}

			hooks, err := git.HooksDir(ctx, main)
			if err != nil {
				return err
			}
			// The hook calls this binary by absolute path (see hook.block).
			// A binary under the temp dir is `go run`: leave it to PATH.
			exe, err := os.Executable()
			if err != nil || strings.HasPrefix(exe, os.TempDir()) {
				exe = ""
			}
			res, err := hook.Install(hooks, exe)
			if err != nil {
				return err
			}
			path := filepath.Join(hooks, "post-checkout")
			switch res {
			case hook.Created:
				fmt.Fprintf(out, "tosk: installed %s\n", path)
			case hook.Chained:
				fmt.Fprintf(out, "tosk: chained after the existing %s\n", path)
			case hook.Updated:
				fmt.Fprintf(out, "tosk: updated the hook in %s\n", path)
			case hook.Present:
				fmt.Fprintf(out, "tosk: hook already installed in %s\n", path)
			}
			return nil
		},
	}
}
