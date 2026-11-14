// Package cli wires the tosk commands.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/spf13/cobra"
)

// Version is set at build time with -ldflags (releases, `make build`).
// `go install …@version` sets no flag: version() then falls back to the
// module version recorded in the binary.
var Version = "dev"

func version() string {
	if Version != "dev" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return Version
}

// errNotImplemented marks commands that exist in the CLI surface but whose
// behaviour is still to be written.
var errNotImplemented = errors.New("not implemented yet")

// Main runs the CLI and returns the process exit code.
//
// Ctrl-C and SIGTERM cancel the context, which stops the git and shell
// commands in flight.
func Main() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := newRoot().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "tosk:", err)
		if errors.Is(err, context.Canceled) {
			return 130
		}
		return 1
	}
	return 0
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "tosk",
		Short:         "Environment preparation for git worktrees",
		Version:       version(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newInit(),
		newSync(),
		newAdd(),
		newRm(),
		newLs(),
		newVar(),
		newRun(),
		newDoctor(),
		newWatch(),
		newHook(),
	)
	return root
}
