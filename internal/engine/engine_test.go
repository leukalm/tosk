package engine

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/leukalm/tosk/internal/config"
	"github.com/leukalm/tosk/internal/git"
	"github.com/leukalm/tosk/internal/state"
)

const testConfig = `
vars:
  port: { provider: port, base: 18100, range: 50 }
  n:    { provider: counter, start: 10 }
  db:   "app_{{slug}}"

profile: { agent: "branch matches '^agent/'" }

create:
  - copy: .env
  - copy: "config/*.local"
  - link: node_modules
  - template: app.conf.tpl
  - replace: { file: .env, match: "^PORT=.*", with: "PORT={{port}}" }
  - write: { file: .tosk-env, content: "DB={{db}}\n" }
  - run: echo create >> run.log
  - run: { cmd: echo seeded >> run.log, when: "exists('seed.sql')" }

sync:
  - run: { cmd: "echo sync $TOSK_DB >> run.log", mode: always }

remove:
  - run: echo removed {{db}} >> ../removed.log

profiles:
  agent:
    create:
      - skip: [link:node_modules]
      - write: { file: agent.txt, content: "{{profile}} {{index}}" }
`

func loadState(t *testing.T, dir string) *state.State {
	t.Helper()
	st, err := state.Load(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func isLink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && isLinkMode(info)
}

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// newRepo creates a repository with a main worktree "app" holding tracked
// and ignored files.
func newRepo(t *testing.T) string {
	t.Helper()
	main := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, main, "init", "-q", "-b", "main")
	write(t, filepath.Join(main, config.FileName), testConfig)
	write(t, filepath.Join(main, "app.conf.tpl"), "port={{port}}\ndb={{db}}\n")
	write(t, filepath.Join(main, ".gitignore"), ".env\nconfig/*.local\nnode_modules\napp.conf\n.tosk-env\nrun.log\nagent.txt\n")
	gitCmd(t, main, "add", ".")
	gitCmd(t, main, "commit", "-q", "-m", "init")
	write(t, filepath.Join(main, ".env"), "PORT=8080\nSECRET=x\n")
	write(t, filepath.Join(main, "config", "a.local"), "a")
	write(t, filepath.Join(main, "config", "b.local"), "b")
	write(t, filepath.Join(main, "node_modules", "pkg", "index.js"), "js")
	return main
}

func newEngine(t *testing.T, main string) (*Engine, *bytes.Buffer) {
	t.Helper()
	cfg, err := config.Load(main)
	if err != nil {
		t.Fatal(err)
	}
	all, err := git.Worktrees(t.Context(), main)
	if err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	e, err := New(cfg, all, Options{Out: out, Err: out})
	if err != nil {
		t.Fatal(err)
	}
	return e, out
}

func worktree(t *testing.T, e *Engine, name string) git.Worktree {
	t.Helper()
	for _, wt := range e.all {
		if filepath.Base(wt.Path) == name {
			return wt
		}
	}
	t.Fatalf("no worktree %s", name)
	return git.Worktree{}
}

func TestSync(t *testing.T) {
	main := newRepo(t)
	gitCmd(t, main, "worktree", "add", "-q", "../one", "-b", "feat/one")
	gitCmd(t, main, "worktree", "add", "-q", "../two", "-b", "feat/two")
	gitCmd(t, main, "worktree", "add", "-q", "../bot", "-b", "agent/bot")

	e, out := newEngine(t, main)
	if err := e.SyncAll(t.Context()); err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	one := worktree(t, e, "one").Path
	two := worktree(t, e, "two").Path
	bot := worktree(t, e, "bot").Path

	// copy, replace, template, write, link
	st1 := loadState(t, one)
	port := st1.Vars["port"]
	if port == "" {
		t.Fatalf("no port allocated\n%s", out)
	}
	if got := read(t, filepath.Join(one, ".env")); got != "PORT="+port+"\nSECRET=x\n" {
		t.Errorf(".env = %q", got)
	}
	if got := read(t, filepath.Join(main, ".env")); got != "PORT=8080\nSECRET=x\n" {
		t.Errorf("main .env was modified: %q", got)
	}
	if read(t, filepath.Join(one, "config", "a.local")) != "a" || read(t, filepath.Join(one, "config", "b.local")) != "b" {
		t.Error("glob copy failed")
	}
	if got := read(t, filepath.Join(one, "app.conf")); got != "port="+port+"\ndb=app_feat_one\n" {
		t.Errorf("app.conf = %q", got)
	}
	if got := read(t, filepath.Join(one, ".tosk-env")); got != "DB=app_feat_one\n" {
		t.Errorf(".tosk-env = %q", got)
	}
	if read(t, filepath.Join(one, "node_modules", "pkg", "index.js")) != "js" || !isLink(filepath.Join(one, "node_modules")) {
		t.Error("node_modules is not a working link")
	}
	// run once, `when` false, sync always
	if got := read(t, filepath.Join(one, "run.log")); got != "create\nsync app_feat_one\n" {
		t.Errorf("run.log = %q", got)
	}

	// unique allocations
	st2, st3 := loadState(t, two), loadState(t, bot)
	if st2.Vars["port"] == port || st3.Vars["port"] == port || st2.Vars["port"] == st3.Vars["port"] {
		t.Errorf("ports are not unique: %s %s %s", port, st2.Vars["port"], st3.Vars["port"])
	}
	mainState := loadState(t, main)
	if mainState.Index != 0 || st1.Index+st2.Index+st3.Index != 6 {
		t.Errorf("indexes: main=%d %d %d %d", mainState.Index, st1.Index, st2.Index, st3.Index)
	}
	counters := map[string]bool{mainState.Vars["n"]: true, st1.Vars["n"]: true, st2.Vars["n"]: true, st3.Vars["n"]: true}
	if len(counters) != 4 || !counters["10"] || !counters["13"] {
		t.Errorf("counters = %v", counters)
	}

	// profile selected by rule: skip + extra step
	if st3.Profile != "agent" || st1.Profile != "" {
		t.Errorf("profiles: bot=%q one=%q", st3.Profile, st1.Profile)
	}
	if exists(filepath.Join(bot, "node_modules")) {
		t.Error("agent profile should skip the node_modules link")
	}
	if got := read(t, filepath.Join(bot, "agent.txt")); got != "agent "+strconv.Itoa(st3.Index) {
		t.Errorf("agent.txt = %q", got)
	}

	// The worktrees stay clean: everything provisioned is ignored.
	status, err := git.Run(t.Context(), one, "status", "--porcelain")
	if err != nil || status != "" {
		t.Errorf("worktree is dirty: %q %v", status, err)
	}

	// Second sync: nothing but the `always` step.
	write(t, filepath.Join(one, ".tosk-env"), "edited by hand, but create ran already\n")
	e, out = newEngine(t, main)
	if err := e.SyncAll(t.Context()); err != nil {
		t.Fatalf("second sync: %v\n%s", err, out)
	}
	if got := read(t, filepath.Join(one, "run.log")); got != "create\nsync app_feat_one\nsync app_feat_one\n" {
		t.Errorf("run.log after second sync = %q", got)
	}
	if loadState(t, one).Vars["port"] != port {
		t.Error("port changed across syncs")
	}
	if strings.Contains(out.String(), "ready") || strings.Contains(out.String(), "write") {
		t.Errorf("second sync redid create work:\n%s", out)
	}
}

// `git worktree add` copies config.worktree from the worktree it runs in:
// the new worktree must not inherit that worktree's record.
func TestStateIsNotInherited(t *testing.T) {
	main := newRepo(t)
	gitCmd(t, main, "worktree", "add", "-q", "../one", "-b", "feat/one")
	e, out := newEngine(t, main)
	if err := e.SyncAll(t.Context()); err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	one := worktree(t, e, "one").Path
	gitCmd(t, main, "worktree", "add", "-q", "../from-main", "-b", "feat/a")
	gitCmd(t, one, "worktree", "add", "-q", "../from-one", "-b", "feat/b")

	e, _ = newEngine(t, main)
	for _, name := range []string{"from-main", "from-one"} {
		c, err := e.Inspect(t.Context(), worktree(t, e, name))
		if err != nil {
			t.Fatal(err)
		}
		if c.State.Created || len(c.State.Vars) != 0 || c.State.HasIndex {
			t.Errorf("%s inherited a record: %+v", name, c.State)
		}
	}
	e, out = newEngine(t, main)
	if err := e.SyncAll(t.Context()); err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	ports, indexes := map[string]bool{}, map[int]bool{}
	for _, wt := range e.all {
		st := loadState(t, wt.Path)
		ports[st.Vars["port"]] = true
		indexes[st.Index] = true
	}
	if len(ports) != 4 || len(indexes) != 4 {
		t.Errorf("ports %v, indexes %v: expected 4 distinct of each", ports, indexes)
	}
	for _, name := range []string{"from-main", "from-one"} {
		if got := read(t, filepath.Join(worktree(t, e, name).Path, "run.log")); !strings.HasPrefix(got, "create\n") {
			t.Errorf("%s: create phase did not run: %q", name, got)
		}
	}
}

func TestRemoveAndRunStep(t *testing.T) {
	main := newRepo(t)
	gitCmd(t, main, "worktree", "add", "-q", "../one", "-b", "feat/one")
	e, out := newEngine(t, main)
	if err := e.SyncAll(t.Context()); err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	wt := worktree(t, e, "one")

	// RunStep ignores the `once` record.
	if err := e.RunStep(t.Context(), wt, "run:echo create >> run.log"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(wt.Path, "run.log")); got != "create\nsync app_feat_one\ncreate\n" {
		t.Errorf("run.log = %q", got)
	}
	if err := e.RunStep(t.Context(), wt, "run:nope"); err == nil {
		t.Error("expected an error for an unknown step")
	}

	if err := e.Remove(t.Context(), wt, false); err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	if got := read(t, filepath.Join(filepath.Dir(main), "removed.log")); got != "removed app_feat_one\n" {
		t.Errorf("removed.log = %q", got)
	}
	if exists(filepath.Join(wt.Path, "node_modules")) {
		t.Error("link should be removed")
	}
	if !exists(filepath.Join(main, "node_modules", "pkg", "index.js")) {
		t.Error("main node_modules was damaged")
	}
	// Ignored files only: git removes the worktree without --force.
	if _, err := git.Run(t.Context(), main, "worktree", "remove", wt.Path); err != nil {
		t.Errorf("git worktree remove: %v", err)
	}
}

func TestFailingStepIsRetried(t *testing.T) {
	main := newRepo(t)
	write(t, filepath.Join(main, config.FileName), "create:\n  - run: echo a >> run.log\n  - run: test -f go\n  - run: echo b >> run.log\n")
	gitCmd(t, main, "worktree", "add", "-q", "../one", "-b", "feat/one")

	e, _ := newEngine(t, main)
	if err := e.SyncAll(t.Context()); err == nil {
		t.Fatal("expected sync to fail")
	}
	one := worktree(t, e, "one").Path
	if loadState(t, one).Created {
		t.Error("worktree must not be marked created")
	}
	write(t, filepath.Join(one, "go"), "")
	e, out := newEngine(t, main)
	if err := e.SyncAll(t.Context()); err != nil {
		t.Fatalf("retry: %v\n%s", err, out)
	}
	if got := read(t, filepath.Join(one, "run.log")); got != "a\nb\n" {
		t.Errorf("run.log = %q (the first step must not run twice)", got)
	}
}

func TestLocal(t *testing.T) {
	for _, p := range []string{"../x", "a/../../x", "/etc/passwd", "..", ""} {
		if got, err := local(p); err == nil && p != "" {
			t.Errorf("local(%q) = %q, want an error", p, got)
		}
	}
	if got, err := local("./a/../b"); err != nil || got != "b" {
		t.Errorf("local = %q, %v", got, err)
	}
}

// Profile rules are tried in file order, not alphabetically; an explicit
// profile (--profile, TOSK_PROFILE) wins over them.
func TestProfileSelection(t *testing.T) {
	main := newRepo(t)
	write(t, filepath.Join(main, config.FileName), `
profile:
  zeta: "branch matches '^feat/'"
  alpha: "branch matches '^feat/'"
profiles:
  zeta: {}
  alpha: {}
  forced: {}
`)
	gitCmd(t, main, "worktree", "add", "-q", "../one", "-b", "feat/one")
	gitCmd(t, main, "worktree", "add", "-q", "../two", "-b", "feat/two")
	cfg, err := config.Load(main)
	if err != nil {
		t.Fatal(err)
	}
	all, err := git.Worktrees(t.Context(), main)
	if err != nil {
		t.Fatal(err)
	}
	pick := func(profile, name string) string {
		t.Helper()
		e, err := New(cfg, all, Options{Profile: profile})
		if err != nil {
			t.Fatal(err)
		}
		c, err := e.Inspect(t.Context(), worktree(t, e, name))
		if err != nil {
			t.Fatal(err)
		}
		return c.Profile
	}
	if got := pick("", "one"); got != "zeta" {
		t.Errorf("profile = %q, want the first rule of the file (zeta)", got)
	}
	if got := pick("forced", "two"); got != "forced" {
		t.Errorf("profile = %q, want the explicit one", got)
	}
	e, err := New(cfg, all, Options{Profile: "nope"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Inspect(t.Context(), worktree(t, e, "two")); err == nil || !strings.Contains(err.Error(), "unknown profile") {
		t.Errorf("unknown profile: err = %v", err)
	}
}

func TestCheck(t *testing.T) {
	main := newRepo(t)
	gitCmd(t, main, "worktree", "add", "-q", "../one", "-b", "feat/one")
	gitCmd(t, main, "worktree", "add", "-q", "../two", "-b", "feat/two")
	e, out := newEngine(t, main)
	if err := e.SyncAll(t.Context()); err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	if got := e.Check(t.Context()); len(got) != 0 {
		t.Fatalf("clean repository has findings: %+v", got)
	}

	// Two worktrees holding the same port, found whatever the order.
	one, two := worktree(t, e, "one").Path, worktree(t, e, "two").Path
	port := loadState(t, one).Vars["port"]
	if err := loadState(t, two).SetVar(t.Context(), "port", port); err != nil {
		t.Fatal(err)
	}
	e, _ = newEngine(t, main)
	var dup []string
	for _, f := range e.Check(t.Context()) {
		if f.Kind == Duplicate && f.Severity == Problem {
			dup = append(dup, f.Message)
		}
	}
	if len(dup) != 1 || !strings.Contains(dup[0], "port "+port+" is allocated more than once: one:port, two:port") {
		t.Errorf("duplicates = %q", dup)
	}
}

// Ctrl-C reaches the commands steps run.
func TestCancelStopsSteps(t *testing.T) {
	main := newRepo(t)
	write(t, filepath.Join(main, config.FileName), "create:\n  - run: sleep 30\n")
	gitCmd(t, main, "worktree", "add", "-q", "../one", "-b", "feat/one")
	e, _ := newEngine(t, main)
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := e.SyncAll(ctx); err == nil {
		t.Fatal("expected sync to fail")
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("sync took %s after cancellation", d)
	}
	if loadState(t, worktree(t, e, "one").Path).Created {
		t.Error("a cancelled worktree must not be marked created")
	}
}

// Tosk's own events go through `git hook run`, which recent git restricts
// to known hook names: a script for tosk-post-create must still run, and
// a repository without one must not fail.
func TestPostCreateEvent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hook scripts need sh")
	}
	main := newRepo(t)
	write(t, filepath.Join(main, config.FileName), "vars:\n  db: \"app_{{slug}}\"\n")
	gitCmd(t, main, "worktree", "add", "-q", "../one", "-b", "feat/one")
	hook := filepath.Join(main, ".git", "hooks", "tosk-post-create")
	write(t, hook, "#!/bin/sh\necho \"$TOSK_DB\" > event.txt\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	e, out := newEngine(t, main)
	if err := e.SyncAll(t.Context()); err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	if got := read(t, filepath.Join(worktree(t, e, "one").Path, "event.txt")); got != "app_feat_one\n" {
		t.Errorf("event.txt = %q", got)
	}
}
