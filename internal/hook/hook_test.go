package hook

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestDetect(t *testing.T) {
	main := t.TempDir()
	gitHooks := filepath.Join(main, ".git", "hooks")

	if s := Detect(main, gitHooks); s.Manager != Plain || s.Versioned || s.File != filepath.Join(gitHooks, "post-checkout") {
		t.Errorf("plain: %+v", s)
	}
	husky := filepath.Join(main, ".husky", "_")
	if s := Detect(main, husky); s.Manager != Husky || !s.Versioned || s.File != filepath.Join(main, ".husky", "post-checkout") {
		t.Errorf("husky: %+v", s)
	}
	githooks := filepath.Join(main, ".githooks")
	if s := Detect(main, githooks); s.Manager != Plain || !s.Versioned {
		t.Errorf("hooksPath in the working tree should be versioned: %+v", s)
	}
	writeFile(t, filepath.Join(main, ".lefthook.yml"), "pre-commit: {}\n")
	if s := Detect(main, gitHooks); s.Manager != Lefthook || s.File != filepath.Join(main, ".lefthook.yml") {
		t.Errorf("lefthook: %+v", s)
	}
}

func TestInstallScript(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hooks")
	s := Setup{Manager: Plain, File: filepath.Join(dir, "post-checkout"), HooksDir: dir}

	exe := "/opt/it's here/tosk"
	if r, err := s.Install(exe); err != nil || r != Created {
		t.Fatalf("first install: %v, %v", r, err)
	}
	if r, err := s.Install(exe); err != nil || r != Present {
		t.Fatalf("second install: %v, %v", r, err)
	}
	if r, err := s.Install("/new/tosk"); err != nil || r != Updated {
		t.Fatalf("install with another path: %v, %v", r, err)
	}
	data := readFile(t, s.File)
	if !strings.Contains(data, "'/new/tosk' hook post-checkout") || strings.Contains(data, "here") {
		t.Errorf("block not rewritten:\n%s", data)
	}
	if !strings.HasPrefix(data, "#!/bin/sh\n") || strings.Count(data, begin) != 1 {
		t.Errorf("unexpected script:\n%s", data)
	}
	if err := s.Check(); err != nil {
		t.Error(err)
	}
}

func TestInstallChains(t *testing.T) {
	dir := t.TempDir()
	s := Setup{Manager: Plain, File: filepath.Join(dir, "post-checkout"), HooksDir: dir}
	writeFile(t, s.File, "#!/bin/sh\necho existing")
	if r, err := s.Install(""); err != nil || r != Chained {
		t.Fatalf("install: %v, %v", r, err)
	}
	data := readFile(t, s.File)
	if !strings.HasPrefix(data, "#!/bin/sh\necho existing\n") || !Installed(data) || strings.Contains(data, "-x") {
		t.Errorf("unexpected script:\n%s", data)
	}
}

// A file committed with the repository gets no path from this machine.
func TestVersionedScriptHasNoMachinePath(t *testing.T) {
	main := t.TempDir()
	s := Detect(main, filepath.Join(main, ".husky", "_"))
	writeFile(t, s.File, "npm run check\n")
	if r, err := s.Install("/Users/me/go/bin/tosk"); err != nil || r != Chained {
		t.Fatalf("install: %v, %v", r, err)
	}
	data := readFile(t, s.File)
	if strings.Contains(data, "/Users/me") || !strings.HasPrefix(data, "npm run check\n") || !Installed(data) {
		t.Errorf("unexpected .husky/post-checkout:\n%s", data)
	}
}

func TestInstallLefthook(t *testing.T) {
	main := t.TempDir()
	hooks := filepath.Join(main, ".git", "hooks")
	config := filepath.Join(main, "lefthook.yml")
	writeFile(t, config, "pre-commit:\n  commands:\n    lint:\n      run: make lint")
	s := Detect(main, hooks)

	if r, err := s.Install("/abs/tosk"); err != nil || r != Chained {
		t.Fatalf("install: %v, %v", r, err)
	}
	data := readFile(t, config)
	if !strings.HasPrefix(data, "pre-commit:\n  commands:\n    lint:\n      run: make lint\n") ||
		!strings.Contains(data, "post-checkout:\n  commands:\n    tosk:\n      run: '"+lefthookCommand+"'\n") ||
		strings.Contains(data, "/abs/tosk") {
		t.Errorf("unexpected config:\n%s", data)
	}
	if r, err := s.Install(""); err != nil || r != Present {
		t.Fatalf("second install: %v, %v", r, err)
	}

	// Registered, but Lefthook has not generated the script yet.
	if err := s.Check(); err == nil || !strings.Contains(err.Error(), "lefthook install") {
		t.Errorf("check before lefthook install: %v", err)
	}
	writeFile(t, filepath.Join(hooks, "post-checkout"), "#!/bin/sh\ncall_lefthook run post-checkout\n")
	if err := s.Check(); err != nil {
		t.Errorf("check: %v", err)
	}
}

// Tosk does not rewrite a config it cannot simply append to.
func TestInstallLefthookManual(t *testing.T) {
	cases := map[string]string{
		"lefthook.yml":  "post-checkout:\n  commands:\n    other:\n      run: echo hi\n",
		"lefthook.toml": "[pre-commit]\n",
	}
	for name, content := range cases {
		main := t.TempDir()
		writeFile(t, filepath.Join(main, name), content)
		s := Detect(main, filepath.Join(main, ".git", "hooks"))
		if r, err := s.Install(""); err != nil || r != Manual {
			t.Errorf("%s: %v, %v", name, r, err)
		}
		if got := readFile(t, filepath.Join(main, name)); got != content {
			t.Errorf("%s was modified:\n%s", name, got)
		}
		if !strings.Contains(s.ManualSteps(), "tosk hook post-checkout {1} {2} {3}") {
			t.Errorf("manual steps: %s", s.ManualSteps())
		}
	}
}

// The generated block must be valid sh and call the quoted absolute path.
func TestBlockRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "it's a dir", "tosk")
	writeFile(t, exe, "#!/bin/sh\necho called \"$@\"\n")
	if err := os.Chmod(exe, 0o755); err != nil {
		t.Fatal(err)
	}
	s := Setup{Manager: Plain, File: filepath.Join(dir, "post-checkout"), HooksDir: dir}
	if _, err := s.Install(exe); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", s.File, "a", "b c").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "called hook post-checkout a b c" {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

func TestIsWorktreeAdd(t *testing.T) {
	null := strings.Repeat("0", 40)
	if !IsWorktreeAdd([]string{null, "abc", "1"}) {
		t.Error("null previous HEAD should be a worktree add")
	}
	if IsWorktreeAdd([]string{"abc123", "def456", "1"}) || IsWorktreeAdd(nil) {
		t.Error("regular checkout is not a worktree add")
	}
}
