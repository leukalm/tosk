// Package vars computes worktree variables and interpolates {{…}}.
package vars

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Vars maps variable names to values.
type Vars map[string]string

// Builtin returns the variables derived from the worktree itself. `index`
// is not included: it is allocated state, not derived.
func Builtin(path, branch, main string) Vars {
	name := filepath.Base(path)
	slugSrc := branch
	if slugSrc == "" { // detached HEAD
		slugSrc = name
	}
	return Vars{
		"name":   name,
		"slug":   Slug(slugSrc),
		"branch": branch,
		"path":   path,
		"main":   main,
	}
}

var nonIdent = regexp.MustCompile(`[^a-z0-9]+`)

// Slug turns a branch name into something safe for identifiers:
// "feat/Invoices-v2" becomes "feat_invoices_v2".
func Slug(s string) string {
	return strings.Trim(nonIdent.ReplaceAllString(strings.ToLower(s), "_"), "_")
}

var placeholder = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

// expand replaces every {{name}} in s with repl(name, value). Unknown
// names are an error. Replacement is single-pass: a value is never expanded
// again.
func (v Vars) expand(s string, repl func(name, val string) string) (string, error) {
	var missing []string
	out := placeholder.ReplaceAllStringFunc(s, func(m string) string {
		name := placeholder.FindStringSubmatch(m)[1]
		val, ok := v[name]
		if !ok {
			missing = append(missing, name)
			return ""
		}
		return repl(name, val)
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("unknown variable: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// Expand replaces every {{name}} in s with the variable's value.
func (v Vars) Expand(s string) (string, error) {
	return v.expand(s, func(_, val string) string { return val })
}

// ExpandQuoted is Expand with every value passed through quote first. Use
// it when s is not plain text (a regular expression, a replacement
// template): values such as a branch name are not trusted.
func (v Vars) ExpandQuoted(s string, quote func(string) string) (string, error) {
	return v.expand(s, func(_, val string) string { return quote(val) })
}

// ShellExpand prepares a shell command line: every {{name}} becomes a
// quoted reference to the variable's environment entry, "${TOSK_NAME}".
// The value itself never becomes shell source, so a branch named
// `x;rm -rf ~` cannot inject a command, whatever quoting surrounds the
// placeholder. The command must run with Env() in its environment.
func (v Vars) ShellExpand(s string) (string, error) {
	return v.expand(s, func(name, _ string) string { return `"${` + EnvName(name) + `}"` })
}

// EnvName returns the environment variable carrying a variable.
func EnvName(name string) string { return "TOSK_" + strings.ToUpper(name) }

// Refs returns the variable names referenced in s.
func Refs(s string) []string {
	var names []string
	for _, m := range placeholder.FindAllStringSubmatch(s, -1) {
		names = append(names, m[1])
	}
	return names
}

// Names returns the variable names, sorted.
func (v Vars) Names() []string {
	names := make([]string, 0, len(v))
	for n := range v {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Env returns the variables as TOSK_<NAME>=value entries, sorted.
func (v Vars) Env() []string {
	env := make([]string, 0, len(v))
	for _, n := range v.Names() {
		env = append(env, EnvName(n)+"="+v[n])
	}
	return env
}
