// Package hook registers Tosk on a repository's post-checkout hook, either
// directly in the hook script or through the hook manager the repository
// already uses (Husky, Lefthook).
package hook

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	begin = "# >>> tosk >>>"
	end   = "# <<< tosk <<<"
)

// Manager is the tool that owns the repository's hooks.
type Manager int

const (
	// Plain: git runs the script in the hooks directory, which Tosk edits.
	Plain Manager = iota
	// Husky 9: git runs generated scripts in .husky/_ that call the
	// versioned .husky/<hook>. The generated ones are rewritten on every
	// install, so Tosk goes in .husky/post-checkout.
	Husky
	// Lefthook: the scripts in the hooks directory are generated from
	// lefthook.yml on `lefthook install`, so Tosk goes in the config.
	Lefthook
)

func (m Manager) String() string {
	switch m {
	case Husky:
		return "Husky"
	case Lefthook:
		return "Lefthook"
	}
	return "git"
}

// Setup is where Tosk is registered in a repository.
type Setup struct {
	Manager Manager
	// File holds the Tosk entry: a hook script, or the Lefthook config.
	File string
	// HooksDir is the directory git runs hooks from.
	HooksDir string
	// Versioned is set when File is committed with the repository: it must
	// not contain anything specific to one machine.
	Versioned bool
}

// lefthookConfigs are the file names Lefthook reads, in its order.
var lefthookConfigs = []string{
	"lefthook.yml", ".lefthook.yml", "lefthook.yaml", ".lefthook.yaml",
	".config/lefthook.yml", ".config/lefthook.yaml",
	"lefthook.toml", ".lefthook.toml", "lefthook.json", ".lefthook.json",
	"lefthook.jsonc", ".lefthook.jsonc",
}

// Detect finds out how hooks are run in the repository whose main worktree
// is mainWorktree, git reading hooks from hooksDir.
func Detect(mainWorktree, hooksDir string) Setup {
	hooksDir = filepath.Clean(hooksDir)
	for _, name := range lefthookConfigs {
		path := filepath.Join(mainWorktree, filepath.FromSlash(name))
		if _, err := os.Stat(path); err == nil {
			return Setup{Manager: Lefthook, File: path, HooksDir: hooksDir, Versioned: true}
		}
	}
	if filepath.Base(hooksDir) == "_" && filepath.Base(filepath.Dir(hooksDir)) == ".husky" {
		return Setup{
			Manager:   Husky,
			File:      filepath.Join(filepath.Dir(hooksDir), "post-checkout"),
			HooksDir:  hooksDir,
			Versioned: true,
		}
	}
	// core.hooksPath may point into the working tree (.githooks, Husky 5
	// to 8's .husky): such a directory is committed.
	versioned := false
	if rel, err := filepath.Rel(mainWorktree, hooksDir); err == nil {
		first := strings.Split(filepath.ToSlash(rel), "/")[0]
		versioned = first != ".." && first != ".git"
	}
	return Setup{Manager: Plain, File: filepath.Join(hooksDir, "post-checkout"), HooksDir: hooksDir, Versioned: versioned}
}

// Result says what Install did.
type Result int

const (
	Created Result = iota // new hook script
	Chained               // added after an existing script or config
	Updated               // the Tosk entry was there and has been rewritten
	Present               // already installed and current
	Manual                // the user must add the entry: see ManualSteps
)

// Install registers Tosk. exe is the absolute path of the tosk binary,
// empty to rely on PATH; it is ignored for versioned files.
func (s Setup) Install(exe string) (Result, error) {
	if s.Versioned {
		exe = ""
	}
	if s.Manager == Lefthook {
		return s.installLefthook()
	}
	return installScript(s.File, block(exe))
}

// block returns the lines added to a post-checkout script.
//
// The hook runs inside the new worktree, whose content comes from the
// branch: finding tosk through PATH alone would let a relative PATH entry
// (".", "bin") run a tosk committed on that branch. So the binary that
// ran `tosk init` is called by absolute path, and PATH is only the
// fallback for when it has moved, or for a script committed with the
// repository, where a path from one machine has no place. Without tosk at
// all the hook is a no-op, so a clone without Tosk installed keeps working.
func block(exe string) string {
	call := ` hook post-checkout "$@"`
	var b strings.Builder
	b.WriteString(begin + "\n")
	if exe != "" {
		q := shellQuote(exe)
		b.WriteString("if [ -x " + q + " ]; then\n  " + q + call + "\nelif")
	} else {
		b.WriteString("if")
	}
	b.WriteString(" command -v tosk >/dev/null 2>&1; then\n  tosk" + call + "\nfi\n" + end + "\n")
	return b.String()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func installScript(path, want string) (Result, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return 0, err
		}
		return Created, os.WriteFile(path, []byte("#!/bin/sh\n"+want), 0o755)
	}
	if err != nil {
		return 0, err
	}
	content := string(data)
	result := Chained
	if i, j := strings.Index(content, begin), strings.Index(content, end); i >= 0 && j > i {
		j += len(end)
		if j < len(content) && content[j] == '\n' {
			j++
		}
		if content[i:j] == want {
			return Present, nil
		}
		content = content[:i] + want + content[j:]
		result = Updated
	} else {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "\n" + want
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		return 0, err
	}
	return result, os.Chmod(path, 0o755)
}

// lefthookCommand is the Tosk command in the Lefthook config. {1} {2} {3}
// are Lefthook's placeholders for the hook's arguments. The guard keeps
// checkouts working for teammates who have not installed Tosk.
const lefthookCommand = `if command -v tosk >/dev/null 2>&1; then tosk hook post-checkout {1} {2} {3}; fi`

const lefthookMarker = "tosk hook post-checkout"

var lefthookHook = regexp.MustCompile(`(?m)^post-checkout\s*:`)

// installLefthook appends a post-checkout section to a YAML config that has
// none. Anything else (an existing post-checkout section, TOML, JSON) is
// left to the user: rewriting their config would lose its layout.
func (s Setup) installLefthook() (Result, error) {
	data, err := os.ReadFile(s.File)
	if err != nil {
		return 0, err
	}
	content := string(data)
	if strings.Contains(content, lefthookMarker) {
		return Present, nil
	}
	ext := filepath.Ext(s.File)
	if (ext != ".yml" && ext != ".yaml") || lefthookHook.MatchString(content) {
		return Manual, nil
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "\n# Tosk: prepare new git worktrees (https://github.com/leukalm/tosk).\n" +
		"post-checkout:\n  commands:\n    tosk:\n      run: '" + lefthookCommand + "'\n"
	info, err := os.Stat(s.File)
	if err != nil {
		return 0, err
	}
	return Chained, os.WriteFile(s.File, []byte(content), info.Mode().Perm())
}

// ManualSteps explains what to add by hand when Install returns Manual.
func (s Setup) ManualSteps() string {
	return fmt.Sprintf(`add this command to the post-checkout hook in %s, then run `+"`lefthook install`"+`:

post-checkout:
  commands:
    tosk:
      run: '%s'`, filepath.Base(s.File), lefthookCommand)
}

// Check reports whether Tosk is registered and git will actually call it.
func (s Setup) Check() error {
	data, err := os.ReadFile(s.File)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	switch s.Manager {
	case Lefthook:
		if !strings.Contains(string(data), lefthookMarker) {
			return fmt.Errorf("tosk is not in the post-checkout hook of %s (run `tosk init`)", s.File)
		}
		// The config only counts once Lefthook has generated the script.
		script, _ := os.ReadFile(filepath.Join(s.HooksDir, "post-checkout"))
		if !strings.Contains(strings.ToLower(string(script)), "lefthook") {
			return fmt.Errorf("the post-checkout hook in %s is not Lefthook's yet (run `lefthook install`)", s.HooksDir)
		}
	default:
		if !Installed(string(data)) {
			return fmt.Errorf("post-checkout hook not installed in %s (run `tosk init`)", s.File)
		}
	}
	return nil
}

// Installed reports whether a hook script already contains the Tosk block.
func Installed(script string) bool {
	return strings.Contains(script, begin)
}

// IsWorktreeAdd reports whether post-checkout arguments describe a fresh
// worktree: git passes a null previous HEAD on `git worktree add`.
func IsWorktreeAdd(args []string) bool {
	return len(args) >= 1 && args[0] != "" && strings.Trim(args[0], "0") == ""
}
