// Package config defines the .tosk.yml schema and loads it.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

// FileName is the config file, at the root of the main worktree.
const FileName = ".tosk.yml"

// Config is a parsed .tosk.yml.
type Config struct {
	Vars map[string]Var `yaml:"vars"`
	// ProfileRules selects a profile for a new worktree: the first rule
	// whose expression matches wins, in file order.
	ProfileRules ProfileRules `yaml:"profile"`
	Phases       `yaml:",inline"`
	Profiles     map[string]Phases `yaml:"profiles"`
}

// ProfileRule selects the profile Name when the `when` expression When
// holds.
type ProfileRule struct {
	Name string
	When string
}

// ProfileRules is the `profile:` mapping, kept in file order: a Go map
// would lose it, and the first matching rule must win.
type ProfileRules []ProfileRule

func (r *ProfileRules) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: profile must be a mapping of profile name to condition", n.Line)
	}
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i], n.Content[i+1]
		if val.Kind != yaml.ScalarNode {
			return fmt.Errorf("line %d: profile rule %q must be a condition string", val.Line, key.Value)
		}
		if seen[key.Value] {
			return fmt.Errorf("line %d: profile rule %q is defined twice", key.Line, key.Value)
		}
		seen[key.Value] = true
		*r = append(*r, ProfileRule{Name: key.Value, When: val.Value})
	}
	return nil
}

// Phases holds the steps of each lifecycle phase. A nil slice means the
// phase was not declared, which matters for `remove` (defaults to the
// reverse of `create`).
type Phases struct {
	Create []Step `yaml:"create"`
	Sync   []Step `yaml:"sync"`
	Remove []Step `yaml:"remove"`
}

// Var is either a plain string with {{…}} interpolation or a provider
// allocation.
type Var struct {
	Value    string
	Provider string
	// Options are the provider's settings (base, range…).
	Options map[string]any
}

// IsProvider reports whether the variable is allocated by a provider.
func (v Var) IsProvider() bool { return v.Provider != "" }

func (v *Var) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		v.Value = n.Value
		return nil
	case yaml.MappingNode:
		if err := n.Decode(&v.Options); err != nil {
			return err
		}
		p, ok := v.Options["provider"].(string)
		if !ok || p == "" {
			return fmt.Errorf("line %d: variable needs a `provider`", n.Line)
		}
		v.Provider = p
		delete(v.Options, "provider")
		return nil
	}
	return fmt.Errorf("line %d: variable must be a string or a provider mapping", n.Line)
}

// Kind is a step type.
type Kind string

const (
	Copy     Kind = "copy"
	Link     Kind = "link"
	Template Kind = "template"
	Replace  Kind = "replace"
	Write    Kind = "write"
	Run      Kind = "run"
	Skip     Kind = "skip"
)

func validKind(k string) bool {
	switch Kind(k) {
	case Copy, Link, Template, Replace, Write, Run, Skip:
		return true
	}
	return false
}

// Run modes.
const (
	ModeOnce   = "once"
	ModeAlways = "always"
)

// Step is one entry of a phase.
type Step struct {
	Kind Kind
	// Arg is the path or glob (copy, link, template) or the command (run).
	Arg string
	// File, Match, With and Content are used by replace and write.
	File    string
	Match   string
	With    string
	Content string
	// Mode is once or always (run only).
	Mode string
	// When is an optional condition.
	When string
	// Skip lists the step names a profile drops from the base config.
	Skip []string
}

// Name identifies a step, e.g. "link:node_modules". It is what `skip` and
// `tosk run` refer to.
func (s Step) Name() string {
	arg := s.Arg
	if arg == "" {
		arg = s.File
	}
	return string(s.Kind) + ":" + arg
}

type stepFields struct {
	Cmd     string `yaml:"cmd"`
	Path    string `yaml:"path"`
	File    string `yaml:"file"`
	Match   string `yaml:"match"`
	With    string `yaml:"with"`
	Content string `yaml:"content"`
	Mode    string `yaml:"mode"`
	When    string `yaml:"when"`
}

func (s *Step) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: step must be a mapping like `copy: .env`", n.Line)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i], n.Content[i+1]
		switch {
		case key.Value == "when":
			s.When = val.Value
		case !validKind(key.Value):
			return fmt.Errorf("line %d: unknown step %q", key.Line, key.Value)
		case s.Kind != "":
			return fmt.Errorf("line %d: step has both %q and %q", key.Line, s.Kind, key.Value)
		default:
			s.Kind = Kind(key.Value)
			if err := s.decodeValue(val); err != nil {
				return err
			}
		}
	}
	if s.Kind == "" {
		return fmt.Errorf("line %d: step has no type", n.Line)
	}
	return s.validate(n.Line)
}

func (s *Step) decodeValue(val *yaml.Node) error {
	switch val.Kind {
	case yaml.ScalarNode:
		s.Arg = val.Value
	case yaml.SequenceNode:
		if s.Kind != Skip {
			return fmt.Errorf("line %d: only `skip` takes a list", val.Line)
		}
		return val.Decode(&s.Skip)
	case yaml.MappingNode:
		var f stepFields
		if err := val.Decode(&f); err != nil {
			return err
		}
		s.Arg = f.Cmd
		if s.Arg == "" {
			s.Arg = f.Path
		}
		s.File, s.Match, s.With, s.Content = f.File, f.Match, f.With, f.Content
		s.Mode = f.Mode
		if f.When != "" {
			s.When = f.When
		}
	}
	return nil
}

func (s *Step) validate(line int) error {
	bad := func(msg string) error { return fmt.Errorf("line %d: %s: %s", line, s.Kind, msg) }
	switch s.Kind {
	case Copy, Link, Template:
		if s.Arg == "" {
			return bad("needs a path")
		}
	case Run:
		if s.Arg == "" {
			return bad("needs a command")
		}
		switch s.Mode {
		case "":
			s.Mode = ModeOnce
		case ModeOnce, ModeAlways:
		default:
			return bad("mode must be once or always")
		}
	case Replace:
		if s.File == "" || s.Match == "" {
			return bad("needs `file` and `match`")
		}
	case Write:
		if s.File == "" {
			return bad("needs `file`")
		}
	case Skip:
		if s.Arg != "" {
			s.Skip, s.Arg = []string{s.Arg}, ""
		}
		if len(s.Skip) == 0 {
			return bad("needs at least one step name")
		}
	}
	return nil
}

// Parse reads a config from YAML.
func Parse(data []byte) (*Config, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var c Config
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	for name := range c.Vars {
		if !varName.MatchString(name) {
			return nil, fmt.Errorf("variable %q: names are lowercase letters, digits and underscores", name)
		}
		if Reserved[name] {
			return nil, fmt.Errorf("variable %q: name is built in", name)
		}
	}
	return &c, nil
}

// Variable names end up in git config keys and environment variables.
var varName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Reserved lists the built-in variable names.
var Reserved = map[string]bool{
	"name": true, "slug": true, "branch": true, "path": true,
	"main": true, "index": true, "profile": true,
}

// Load reads .tosk.yml from the root of the main worktree.
func Load(mainWorktree string) (*Config, error) {
	path := filepath.Join(mainWorktree, FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}
