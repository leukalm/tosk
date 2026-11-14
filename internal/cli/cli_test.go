package cli

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// newRepo creates a repository "app" with the given .tosk.yml and makes it
// the current directory. It returns the main worktree's path, resolved so
// that it compares equal to what git reports (t.TempDir may sit behind a
// symlink, e.g. /var on macOS).
func newRepo(t *testing.T, config string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "app")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, main, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(main, ".tosk.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, main, "add", ".")
	gitCmd(t, main, "commit", "-q", "-m", "init")
	t.Chdir(main)
	return main
}

// run executes a tosk command line in the current directory.
func run(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	root := newRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err = root.ExecuteContext(t.Context())
	return out.String(), err
}

func TestAddDefaultPath(t *testing.T) {
	main := newRepo(t, "create: []\n")
	if out, err := run(t, "add", "feat/Invoices"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	// Next to the main worktree: <main>-<slug of the branch>.
	want := filepath.Join(filepath.Dir(main), "app-feat-invoices")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("worktree not created at %s: %v", want, err)
	}
	out, err := run(t, "ls")
	if err != nil || !strings.Contains(out, "app-feat-invoices") || !strings.Contains(out, "feat/Invoices") {
		t.Errorf("ls: %v\n%s", err, out)
	}
}

func TestAddExplicitPath(t *testing.T) {
	main := newRepo(t, "create: []\n")
	path := filepath.Join(filepath.Dir(main), "elsewhere")
	if out, err := run(t, "add", "feat/x", path); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error(err)
	}
}

func TestRmRefusesMainWorktree(t *testing.T) {
	main := newRepo(t, "create: []\n")
	for _, arg := range []string{main, "app", "main", "."} {
		_, err := run(t, "rm", arg)
		if err == nil || !strings.Contains(err.Error(), "main worktree") {
			t.Errorf("rm %s: err = %v", arg, err)
		}
	}
	if _, err := os.Stat(main); err != nil {
		t.Fatal("main worktree is gone")
	}
}

func TestRmRemovesWorktree(t *testing.T) {
	main := newRepo(t, "create: []\n")
	if out, err := run(t, "add", "feat/x"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	wt := filepath.Join(filepath.Dir(main), "app-feat-x")
	if out, err := run(t, "rm", "feat/x"); err != nil {
		t.Fatalf("rm: %v\n%s", err, out)
	}
	if _, err := os.Stat(wt); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("worktree still there: %v", err)
	}
}

// git refuses to remove a worktree with untracked files. tosk rm must find
// that out before the remove phase, which is destructive.
// Claude Code locks the worktrees it creates. rm refuses them before the
// remove phase, and --force removes them for real.
func TestRmLockedWorktree(t *testing.T) {
	main := newRepo(t, "remove:\n  - run: echo dropped >> ../removed.log\n")
	if out, err := run(t, "add", "feat/x"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	wt := filepath.Join(filepath.Dir(main), "app-feat-x")
	log := filepath.Join(filepath.Dir(main), "removed.log")
	gitCmd(t, main, "worktree", "lock", "--reason", "claude session", wt)

	if _, err := run(t, "rm", "feat/x"); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Errorf("rm: err = %v", err)
	}
	if _, err := os.Stat(log); !errors.Is(err, fs.ErrNotExist) {
		t.Error("the remove phase ran although the worktree stays")
	}
	if out, err := run(t, "rm", "--force", "feat/x"); err != nil {
		t.Fatalf("rm --force: %v\n%s", err, out)
	}
	if _, err := os.Stat(wt); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("worktree still there: %v", err)
	}
	if data, _ := os.ReadFile(log); string(data) != "dropped\n" {
		t.Errorf("removed.log = %q", data)
	}
}

func TestRmDirtyWorktreeRunsNothing(t *testing.T) {
	main := newRepo(t, "remove:\n  - run: echo dropped >> ../removed.log\n")
	if out, err := run(t, "add", "feat/x"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	wt := filepath.Join(filepath.Dir(main), "app-feat-x")
	log := filepath.Join(filepath.Dir(main), "removed.log")
	if err := os.WriteFile(filepath.Join(wt, "untracked.txt"), []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := run(t, "rm", "feat/x")
	if err == nil || !strings.Contains(err.Error(), "untracked.txt") {
		t.Errorf("rm: err = %v", err)
	}
	if _, err := os.Stat(log); !errors.Is(err, fs.ErrNotExist) {
		t.Error("the remove phase ran although the worktree stays")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("worktree should still be there: %v", err)
	}

	if out, err := run(t, "rm", "--force", "feat/x"); err != nil {
		t.Fatalf("rm --force: %v\n%s", err, out)
	}
	if data, _ := os.ReadFile(log); string(data) != "dropped\n" {
		t.Errorf("removed.log = %q", data)
	}
	if _, err := os.Stat(wt); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("worktree still there: %v", err)
	}
}

func TestVarUnallocated(t *testing.T) {
	newRepo(t, "vars:\n  port: { provider: port, base: 18300, range: 10 }\n  db: \"app_{{slug}}\"\n")
	_, err := run(t, "var", "port")
	if err == nil || !strings.Contains(err.Error(), "not allocated yet") {
		t.Errorf("var port: err = %v", err)
	}
	// Reading never allocates.
	if _, err := run(t, "var", "port"); err == nil {
		t.Error("var allocated a value")
	}
	if _, err := run(t, "var", "nope"); err == nil || !strings.Contains(err.Error(), "unknown variable") {
		t.Errorf("var nope: err = %v", err)
	}
	out, err := run(t, "var", "db")
	if err != nil || out != "app_main\n" {
		t.Errorf("var db = %q, %v", out, err)
	}
}

func TestProfileFromEnvironment(t *testing.T) {
	main := newRepo(t, "profiles:\n  agent:\n    create:\n      - write: { file: who.txt, content: \"{{profile}}\" }\n")
	t.Setenv("TOSK_PROFILE", "agent")
	if out, err := run(t, "add", "feat/x"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(main), "app-feat-x", "who.txt"))
	if err != nil || string(data) != "agent" {
		t.Errorf("who.txt = %q, %v", data, err)
	}
}

func TestHookPostCheckoutSkip(t *testing.T) {
	main := newRepo(t, "create:\n  - write: { file: made.txt, content: x }\n")
	gitCmd(t, main, "worktree", "add", "-q", "../one", "-b", "feat/one")
	one := filepath.Join(filepath.Dir(main), "one")
	nullHead := strings.Repeat("0", 40)
	args := []string{"hook", "post-checkout", nullHead, "abc", "1"}

	t.Chdir(one)
	t.Setenv(skipHookEnv, "1")
	if out, err := run(t, args...); err != nil {
		t.Fatalf("hook: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(one, "made.txt")); err == nil {
		t.Fatal("hook ran although " + skipHookEnv + " is set")
	}

	t.Setenv(skipHookEnv, "")
	if out, err := run(t, args...); err != nil {
		t.Fatalf("hook: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(one, "made.txt")); err != nil {
		t.Errorf("hook did not prepare the worktree: %v", err)
	}
}

func TestDoctorCountsProblems(t *testing.T) {
	newRepo(t, "create: []\n")
	// No hook installed: at least one failure, reported with its count.
	out, err := run(t, "doctor")
	if err == nil || !strings.Contains(err.Error(), "problem") {
		t.Errorf("doctor: err = %v\n%s", err, out)
	}
	if !strings.Contains(out, "post-checkout hook not installed") || !strings.Contains(out, "no duplicate allocation") {
		t.Errorf("doctor output:\n%s", out)
	}
}

func TestNotInRepository(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := run(t, "ls"); err == nil || !strings.Contains(err.Error(), "not inside a git worktree") {
		t.Errorf("err = %v", err)
	}
}

func TestSkill(t *testing.T) {
	main := newRepo(t, "create: []\n")
	path := filepath.Join(main, ".claude", "skills", "tosk", "SKILL.md")

	out, err := run(t, "skill")
	if err != nil || !strings.Contains(out, "wrote") {
		t.Fatalf("skill: %v\n%s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(data), "---\nname: tosk\ndescription: ") {
		t.Fatalf("SKILL.md: %v\n%.80s", err, data)
	}
	if out, err := run(t, "skill"); err != nil || !strings.Contains(out, "up to date") {
		t.Errorf("second run: %v\n%s", err, out)
	}
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, "skill"); err != nil || !strings.Contains(out, "updated") {
		t.Errorf("update: %v\n%s", err, out)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := run(t, "skill", "--global"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "tosk", "SKILL.md")); err != nil {
		t.Error(err)
	}
	if out, err := run(t, "skill", "--print"); err != nil || !strings.Contains(out, "tosk var port") {
		t.Errorf("print: %v\n%.80s", err, out)
	}
}
