package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"

	"github.com/leukalm/tosk/internal/git"
)

// Allocations must not race when several worktrees are created at once
// (agents do that). The lock is held only while variables are resolved,
// never while steps run.
//
// It is an OS advisory lock on a file in the git common directory: the
// kernel releases it when the process exits, however it exits, so there is
// no stale lock to take over. The file itself stays.
const lockTimeout = 2 * time.Minute

func (e *Engine) lock(ctx context.Context) (unlock func(), err error) {
	common, err := git.CommonDir(ctx, e.main.Path)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(common, "tosk.lock")
	fl := flock.New(path)
	ctx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()
	if _, err := fl.TryLockContext(ctx, 50*time.Millisecond); err != nil {
		return nil, fmt.Errorf("waiting for another tosk to release %s: %w", path, err)
	}
	return func() { _ = fl.Unlock() }, nil
}
