package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/leukalm/tosk/internal/config"
)

// A worktree holds what its branch committed, symlinks included. No step
// may read or write through one that leaves the worktree.
func TestSymlinksCannotEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs symlinks")
	}
	cases := []struct {
		name   string
		config string
		// link is created in the worktree, pointing to target under the
		// outside directory.
		link, target string
	}{
		{"template output", "create:\n  - template: app.conf.tpl\n", "app.conf", "victim"},
		{"write through a dangling link", "create:\n  - write: { file: .tosk-env, content: pwned }\n", ".tosk-env", "created"},
		{"write under a linked directory", "create:\n  - write: { file: dir/file, content: pwned }\n", "dir", "."},
		{"replace", "create:\n  - replace: { file: target.txt, match: original, with: pwned }\n", "target.txt", "victim"},
		{"copy under a linked directory", "create:\n  - copy: \"config/*.local\"\n", "config", "."},
		{"template source", "create:\n  - template: leak.tpl\n", "leak.tpl", "victim"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			main := newRepo(t)
			write(t, filepath.Join(main, config.FileName), tc.config)
			outside := t.TempDir()
			write(t, filepath.Join(outside, "victim"), "original")
			gitCmd(t, main, "worktree", "add", "-q", "../one", "-b", "feat/one")
			e, out := newEngine(t, main)
			one := worktree(t, e, "one").Path
			if err := os.Symlink(filepath.Join(outside, tc.target), filepath.Join(one, tc.link)); err != nil {
				t.Fatal(err)
			}

			if err := e.SyncAll(t.Context()); err == nil {
				t.Errorf("sync should refuse to follow the link\n%s", out)
			}
			if got := read(t, filepath.Join(outside, "victim")); got != "original" {
				t.Errorf("file outside the worktree was modified: %q", got)
			}
			entries, err := os.ReadDir(outside)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Errorf("files were created outside the worktree: %v", entries)
			}
			// Nothing read from outside may land in the worktree either.
			if data, err := os.ReadFile(filepath.Join(one, "leak")); err == nil {
				t.Errorf("outside content was rendered into the worktree: %q", data)
			}
		})
	}
}

// Branch names are attacker-chosen and may contain shell syntax. They must
// reach commands, patterns and replacements as plain data.
func TestBranchNameCannotInject(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("branch name is not a valid Windows path component")
	}
	main := newRepo(t)
	write(t, filepath.Join(main, config.FileName), `
vars:
  tag: "v-{{branch}}"
create:
  - run: echo bare {{branch}} > out.txt
  - run: echo "double {{branch}}" >> out.txt
  - run: echo "user var {{tag}}" >> out.txt
  - write: { file: f.txt, content: "B=old\n" }
  - replace: { file: f.txt, match: "^B=.*", with: "B={{branch}}" }
  - write: { file: g.txt, content: "{{branch}} stays\n" }
  - replace: { file: g.txt, match: "{{branch}}", with: gone }
`)
	branch := "x$0.+;touch${IFS}PWNED;$(touch${IFS}PWNED2)`touch${IFS}PWNED3`"
	gitCmd(t, main, "worktree", "add", "-q", "../evil", "-b", branch)
	e, out := newEngine(t, main)
	if err := e.SyncAll(t.Context()); err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	evil := worktree(t, e, "evil").Path
	entries, _ := os.ReadDir(evil)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "PWNED") {
			t.Errorf("command injected through the branch name: %s exists", entry.Name())
		}
	}
	want := "bare " + branch + "\ndouble " + branch + "\nuser var v-" + branch + "\n"
	if got := read(t, filepath.Join(evil, "out.txt")); got != want {
		t.Errorf("out.txt = %q\nwant      %q", got, want)
	}
	// `$0` in the value is not a capture reference…
	if got := read(t, filepath.Join(evil, "f.txt")); got != "B="+branch+"\n" {
		t.Errorf("f.txt = %q", got)
	}
	// …and `.+` in the value is not a pattern.
	if got := read(t, filepath.Join(evil, "g.txt")); got != "gone stays\n" {
		t.Errorf("g.txt = %q", got)
	}
}

func TestScriptProvider(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs an executable script")
	}
	main := newRepo(t)
	write(t, filepath.Join(main, config.FileName), "vars:\n  schema: { provider: ./alloc.sh }\n")
	script := filepath.Join(main, "alloc.sh")
	write(t, script, `#!/bin/sh
case "$1" in
  allocate) echo "s $TOSK_SLUG" ;;
  release)  echo "$2|$3|$TOSK_VALUE" >> ../released.log ;;
esac
`)
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, main, "add", "alloc.sh")
	gitCmd(t, main, "commit", "-q", "-m", "provider")
	gitCmd(t, main, "worktree", "add", "-q", "../one", "-b", "feat/one")

	e, out := newEngine(t, main)
	if err := e.SyncAll(t.Context()); err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	wt := worktree(t, e, "one")
	if got := loadState(t, wt.Path).Vars["schema"]; got != "s feat_one" {
		t.Errorf("schema = %q", got)
	}
	if err := e.Remove(t.Context(), wt, false); err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	if got := read(t, filepath.Join(filepath.Dir(main), "released.log")); got != "schema|s feat_one|s feat_one\n" {
		t.Errorf("released.log = %q", got)
	}
}

// Worktrees prepared at the same time (agents) must not get the same port
// or index.
func TestConcurrentSyncsAllocateUniquely(t *testing.T) {
	main := newRepo(t)
	write(t, filepath.Join(main, config.FileName), "vars:\n  port: { provider: port, base: 18200, range: 50 }\n  n: { provider: counter }\n")
	names := []string{"a", "b", "c", "d", "e", "f"}
	for _, n := range names {
		gitCmd(t, main, "worktree", "add", "-q", "../"+n, "-b", n)
	}
	var wg sync.WaitGroup
	errs := make([]error, len(names))
	for i, n := range names {
		e, _ := newEngine(t, main) // one engine per goroutine, as one process per hook
		wt := worktree(t, e, n)
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = e.SyncOne(t.Context(), wt)
		}()
	}
	wg.Wait()
	ports, counters, indexes := map[string]bool{}, map[string]bool{}, map[int]bool{}
	e, _ := newEngine(t, main)
	for i, n := range names {
		if errs[i] != nil {
			t.Fatalf("%s: %v", n, errs[i])
		}
		st := loadState(t, worktree(t, e, n).Path)
		ports[st.Vars["port"]] = true
		counters[st.Vars["n"]] = true
		indexes[st.Index] = true
	}
	if len(ports) != len(names) || len(counters) != len(names) || len(indexes) != len(names) {
		t.Errorf("duplicates: ports %v, counters %v, indexes %v", ports, counters, indexes)
	}
}
