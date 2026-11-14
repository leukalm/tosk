package hook

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstall(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hooks")
	path := filepath.Join(dir, "post-checkout")

	exe := "/opt/it's here/tosk"
	if r, err := Install(dir, exe); err != nil || r != Created {
		t.Fatalf("first install: %v, %v", r, err)
	}
	if r, err := Install(dir, exe); err != nil || r != Present {
		t.Fatalf("second install: %v, %v", r, err)
	}
	if r, err := Install(dir, "/new/tosk"); err != nil || r != Updated {
		t.Fatalf("install with another path: %v, %v", r, err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "'/new/tosk' hook post-checkout") || strings.Contains(string(data), "here") {
		t.Errorf("block not rewritten:\n%s", data)
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), "#!/bin/sh\n") || strings.Count(string(data), begin) != 1 {
		t.Errorf("unexpected script:\n%s", data)
	}
}

func TestInstallChains(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "post-checkout")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho existing"), 0o755); err != nil {
		t.Fatal(err)
	}
	if r, err := Install(dir, ""); err != nil || r != Chained {
		t.Fatalf("install: %v, %v", r, err)
	}
	data, _ := os.ReadFile(path)
	s := string(data)
	if !strings.HasPrefix(s, "#!/bin/sh\necho existing\n") || !Installed(s) || strings.Contains(s, "-x") {
		t.Errorf("unexpected script:\n%s", s)
	}
}

// The generated block must be valid sh and call the quoted absolute path.
func TestBlockRuns(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "it's a dir", "tosk")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho called \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir, exe); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", filepath.Join(dir, "post-checkout"), "a", "b c").CombinedOutput()
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
