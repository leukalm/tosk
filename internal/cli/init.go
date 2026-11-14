package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
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

# remove: steps for what lives outside the worktree (databases, containers).
# Links are always removed; the rest goes with the directory.
`

// huskyCopy is added to a new .tosk.yml in a Husky repository. Husky 9's
// hooks live in .husky/_, generated and gitignored: a worktree has none
// until something creates them, and git finds no hook at all when a
// worktree is added from it. Copying them makes every worktree a place
// where `git worktree add` works.
const huskyCopy = `create:
  - copy: .husky/_   # Husky's generated hooks, so worktrees added from this one run them
`

func newInit() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create .tosk.yml and install the hook",
		Long: `Create .tosk.yml and register Tosk on the post-checkout hook.

With Husky, Tosk is added to .husky/post-checkout. With Lefthook, a
post-checkout command is added to its config and ` + "`lefthook install`" + ` is run.
Otherwise Tosk is appended to the post-checkout script git runs.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			r, err := openRepo(ctx)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			main := r.Main().Path

			hooks, err := git.HooksDir(ctx, main)
			if err != nil {
				return err
			}
			setup := hook.Detect(main, hooks)

			cfg := filepath.Join(main, config.FileName)
			if _, err := os.Stat(cfg); errors.Is(err, fs.ErrNotExist) {
				content := starterConfig
				if setup.Manager == hook.Husky {
					content = strings.Replace(content, "create: []\n", huskyCopy, 1)
				}
				if err := os.WriteFile(cfg, []byte(content), 0o644); err != nil {
					return err
				}
				fmt.Fprintf(out, "tosk: wrote %s\n", cfg)
			} else if err != nil {
				return err
			} else {
				fmt.Fprintf(out, "tosk: %s already exists, left as is\n", cfg)
				if setup.Manager == hook.Husky {
					fmt.Fprintln(out, "tosk: with Husky, add `- copy: .husky/_` to create, so that worktrees added from a worktree run hooks too")
				}
			}

			// The hook calls this binary by absolute path (see hook.block).
			// A binary under the temp dir is `+"`go run`"+`: leave it to PATH.
			exe, err := os.Executable()
			if err != nil || strings.HasPrefix(exe, os.TempDir()) {
				exe = ""
			}
			res, err := setup.Install(exe)
			if err != nil {
				return err
			}
			via := ""
			if setup.Manager != hook.Plain {
				via = " (" + setup.Manager.String() + ")"
			}
			switch res {
			case hook.Created:
				fmt.Fprintf(out, "tosk: installed %s%s\n", setup.File, via)
			case hook.Chained:
				fmt.Fprintf(out, "tosk: added to %s%s\n", setup.File, via)
			case hook.Updated:
				fmt.Fprintf(out, "tosk: updated %s%s\n", setup.File, via)
			case hook.Present:
				fmt.Fprintf(out, "tosk: already in %s%s\n", setup.File, via)
			case hook.Manual:
				return fmt.Errorf("could not edit %s: %s", setup.File, setup.ManualSteps())
			}
			if setup.Manager == hook.Lefthook {
				return lefthookInstall(cmd, main)
			}
			return nil
		},
	}
}

// lefthookInstall regenerates Lefthook's hook scripts from its config, which
// is what makes the new post-checkout command run.
func lefthookInstall(cmd *cobra.Command, dir string) error {
	bin, err := exec.LookPath("lefthook")
	if err != nil {
		fmt.Fprintln(cmd.OutOrStdout(), "tosk: run `lefthook install` to activate the hook (lefthook is not on PATH)")
		return nil
	}
	c := exec.CommandContext(cmd.Context(), bin, "install")
	c.Dir = dir
	c.Env = git.CleanEnv()
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("lefthook install: %w\n%s", err, out)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "tosk: ran `lefthook install`")
	return nil
}
