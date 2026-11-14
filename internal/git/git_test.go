package git

import (
	"path/filepath"
	"testing"
)

func TestParseWorktrees(t *testing.T) {
	out := `worktree /work/app
HEAD 3f2a9c1000000000000000000000000000000000
branch refs/heads/main

worktree /work/app-feat-invoices
HEAD 1111111000000000000000000000000000000000
branch refs/heads/feat/invoices
locked reason why

worktree /work/app-detached
HEAD 2222222000000000000000000000000000000000
detached
prunable gitdir file points to non-existent location
`
	got := ParseWorktrees(out)
	if len(got) != 3 {
		t.Fatalf("got %d worktrees", len(got))
	}
	if got[0].Path != filepath.FromSlash("/work/app") || got[0].Branch != "main" {
		t.Errorf("main = %+v", got[0])
	}
	if got[1].Branch != "feat/invoices" || !got[1].Locked {
		t.Errorf("second = %+v", got[1])
	}
	if !got[2].Detached || !got[2].Prunable || got[2].Branch != "" {
		t.Errorf("third = %+v", got[2])
	}
}
