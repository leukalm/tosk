package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/leukalm/tosk/plugin/skills"
)

func newSkill() *cobra.Command {
	var global, printOnly bool
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Install the Tosk skill for coding agents",
		Long: `Write the Tosk Agent Skill, which teaches coding agents such as Claude Code to
use the worktree's own ports and names, and to write and debug .tosk.yml.

By default it goes in .claude/skills/tosk/ of the repository: commit it, and
every agent working on the repository has it. With --global it goes in
~/.claude/skills/tosk/ instead, for all your repositories. The skill matches
this version of tosk: run the command again after upgrading.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if printOnly {
				_, err := fmt.Fprint(cmd.OutOrStdout(), skills.Tosk)
				return err
			}
			var base string
			if global {
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				base = home
			} else {
				r, err := openRepo(cmd.Context())
				if err != nil {
					return err
				}
				base = r.Main().Path
			}
			path := filepath.Join(base, ".claude", "skills", "tosk", "SKILL.md")
			old, err := os.ReadFile(path)
			switch {
			case err == nil && bytes.Equal(old, []byte(skills.Tosk)):
				fmt.Fprintf(cmd.OutOrStdout(), "tosk: %s is up to date\n", path)
				return nil
			case err != nil && !errors.Is(err, fs.ErrNotExist):
				return err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(skills.Tosk), 0o644); err != nil {
				return err
			}
			verb := "wrote"
			if old != nil {
				verb = "updated"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "tosk: %s %s\n", verb, path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&global, "global", false, "install in ~/.claude/skills instead of the repository")
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the skill instead of writing it")
	return cmd
}
