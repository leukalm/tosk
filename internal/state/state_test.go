package state

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// repo returns a main worktree with per-worktree config on.
func repo(t *testing.T) string {
	t.Helper()
	main := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, main, "init", "-q", "-b", "main")
	gitCmd(t, main, "commit", "-q", "--allow-empty", "-m", "init")
	if err := Enable(t.Context(), main); err != nil {
		t.Fatal(err)
	}
	return main
}

func TestEnable(t *testing.T) {
	main := repo(t)
	on, err := Enabled(t.Context(), main)
	if err != nil || !on {
		t.Fatalf("Enabled = %v, %v", on, err)
	}
	other := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, other, "init", "-q")
	if on, err := Enabled(t.Context(), other); err != nil || on {
		t.Errorf("Enabled on a fresh repository = %v, %v", on, err)
	}
}

// No config.worktree is a worktree to prepare, not an error.
func TestLoadWithoutRecord(t *testing.T) {
	st, err := Load(t.Context(), repo(t))
	if err != nil {
		t.Fatal(err)
	}
	if st.Created || st.HasIndex || len(st.Vars) != 0 || len(st.Done) != 0 {
		t.Errorf("state = %+v", st)
	}
}

func TestLoadFailureIsAnError(t *testing.T) {
	main := repo(t)
	// A config.worktree git cannot parse must not read as "no record".
	if err := os.WriteFile(filepath.Join(main, ".git", "config.worktree"), []byte("[tosk\nbroken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, err := Load(t.Context(), main); err == nil {
		t.Errorf("expected an error, got %+v", st)
	}
}

func TestRoundTrip(t *testing.T) {
	main := repo(t)
	st, err := Load(t.Context(), main)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	for _, err := range []error{
		st.SetIndex(ctx, 3),
		st.SetProfile(ctx, "agent"),
		st.SetVar(ctx, "db_name", "app_x"), // underscores are stored as dashes
		st.SetVar(ctx, "port", "8100"),
		st.MarkDone(ctx, "create:run:a"),
		st.MarkDone(ctx, "create:run:a"), // idempotent
		st.MarkDone(ctx, "create:run:b"),
		st.SetCreated(ctx),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := Load(ctx, main)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Created || got.Profile != "agent" || !got.HasIndex || got.Index != 3 {
		t.Errorf("state = %+v", got)
	}
	if got.Vars["db_name"] != "app_x" || got.Vars["port"] != "8100" || len(got.Vars) != 2 {
		t.Errorf("vars = %v", got.Vars)
	}
	if !got.Done["create:run:a"] || !got.Done["create:run:b"] || len(got.Done) != 2 {
		t.Errorf("done = %v", got.Done)
	}
}

// `git worktree add` copies the config.worktree of the worktree it runs
// from. That record belongs to someone else: it reads as empty, and the
// first write wipes it.
func TestInheritedRecordIsIgnoredAndWiped(t *testing.T) {
	main := repo(t)
	ctx := t.Context()
	st, err := Load(ctx, main)
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{st.SetIndex(ctx, 0), st.SetVar(ctx, "port", "8100"), st.SetCreated(ctx)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	gitCmd(t, main, "worktree", "add", "-q", "../one", "-b", "feat/one")
	one := filepath.Join(filepath.Dir(main), "one")

	inherited, err := Load(ctx, one)
	if err != nil {
		t.Fatal(err)
	}
	if inherited.Created || inherited.HasIndex || len(inherited.Vars) != 0 {
		t.Fatalf("inherited the main record: %+v", inherited)
	}
	if !inherited.inherited || inherited.owned {
		t.Errorf("inherited = %v, owned = %v", inherited.inherited, inherited.owned)
	}

	if err := inherited.SetIndex(ctx, 1); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(ctx, one)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.owned || reloaded.Index != 1 || reloaded.Created || len(reloaded.Vars) != 0 {
		t.Errorf("after the first write: %+v", reloaded)
	}
	// The main worktree's record is untouched.
	again, err := Load(ctx, main)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Created || again.Vars["port"] != "8100" {
		t.Errorf("main record changed: %+v", again)
	}
}
