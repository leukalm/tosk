// Tosk prepares git worktrees: it copies what git ignores, renders local
// config, allocates what must be unique and runs setup commands.
package main

import (
	"os"

	"github.com/leukalm/tosk/internal/cli"
)

func main() {
	os.Exit(cli.Main())
}
