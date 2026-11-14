// Package engine reconciles worktrees with the config: it resolves
// variables, allocates providers and runs the phases.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/leukalm/tosk/internal/config"
	"github.com/leukalm/tosk/internal/expr"
	"github.com/leukalm/tosk/internal/git"
	"github.com/leukalm/tosk/internal/state"
	"github.com/leukalm/tosk/internal/vars"
)

// Engine works on one repository.
type Engine struct {
	cfg     *config.Config
	main    git.Worktree
	all     []git.Worktree
	out     io.Writer
	err     io.Writer
	profile string

	states map[string]*state.State
}

// Options configures an Engine.
type Options struct {
	// Out and Err receive progress and errors; nil discards them.
	Out, Err io.Writer
	// Profile, when set, is used for worktrees not created yet: it wins
	// over the config's profile rules.
	Profile string
}

// New returns an Engine for a repository. worktrees is the output of
// git.Worktrees: the main worktree comes first.
func New(cfg *config.Config, worktrees []git.Worktree, opts Options) (*Engine, error) {
	if len(worktrees) == 0 {
		return nil, errors.New("no worktree")
	}
	e := &Engine{cfg: cfg, main: worktrees[0], all: worktrees, out: opts.Out, err: opts.Err, profile: opts.Profile}
	if e.out == nil {
		e.out = io.Discard
	}
	if e.err == nil {
		e.err = io.Discard
	}
	return e, nil
}

// Target is a worktree with everything resolved.
type Target struct {
	WT      git.Worktree
	IsMain  bool
	State   *state.State
	Vars    vars.Vars
	Profile string
	Phases  config.Phases
	// RemoveDeclared reports whether a remove phase is declared.
	RemoveDeclared bool
	// Pending lists the variables that are not allocated yet.
	Pending []string

	header string
	out    io.Writer
}

// say prints a progress line, preceded once by the worktree header.
func (c *Target) say(label, format string, args ...any) {
	if c.header != "" {
		fmt.Fprintf(c.out, "tosk: [%s]\n", c.header)
		c.header = ""
	}
	fmt.Fprintf(c.out, "tosk: %-5s %s\n", label, fmt.Sprintf(format, args...))
}

func (e *Engine) state(ctx context.Context, path string) (*state.State, error) {
	if s, ok := e.states[path]; ok {
		return s, nil
	}
	s, err := state.Load(ctx, path)
	if err != nil {
		return nil, err
	}
	if e.states == nil {
		e.states = map[string]*state.State{}
	}
	e.states[path] = s
	return s, nil
}

// others returns the state of every other live worktree.
func (e *Engine) others(ctx context.Context, wt git.Worktree) ([]*state.State, error) {
	var list []*state.State
	for _, o := range e.all {
		if o.Path == wt.Path || o.Bare || o.Prunable {
			continue
		}
		st, err := e.state(ctx, o.Path)
		if err != nil {
			return nil, err
		}
		list = append(list, st)
	}
	return list, nil
}

// Inspect computes the variables, profile and phases of a worktree without
// writing anything: allocations that were not made yet are listed in
// Target.Pending.
func (e *Engine) Inspect(ctx context.Context, wt git.Worktree) (*Target, error) {
	return e.resolve(ctx, wt, false)
}

// resolve computes a Target. With allocate, missing allocations are made
// and stored (the caller holds the lock); without, they are reported in
// Target.Pending.
func (e *Engine) resolve(ctx context.Context, wt git.Worktree, allocate bool) (*Target, error) {
	st, err := e.state(ctx, wt.Path)
	if err != nil {
		return nil, err
	}
	c := &Target{WT: wt, IsMain: wt.Path == e.main.Path, State: st, out: e.out}
	v := vars.Builtin(wt.Path, wt.Branch, e.main.Path)
	c.Vars = v

	// index: 0 for the main worktree, then 1, 2, 3…
	switch {
	case st.HasIndex:
		v["index"] = strconv.Itoa(st.Index)
	case allocate:
		idx := 0
		if !c.IsMain {
			others, err := e.others(ctx, wt)
			if err != nil {
				return nil, err
			}
			used := map[int]bool{}
			for _, o := range others {
				if o.HasIndex {
					used[o.Index] = true
				}
			}
			for idx = 1; used[idx]; idx++ {
			}
		}
		if err := st.SetIndex(ctx, idx); err != nil {
			return nil, err
		}
		v["index"] = strconv.Itoa(idx)
	default:
		c.Pending = append(c.Pending, "index")
	}

	profile, err := e.chooseProfile(c)
	if err != nil {
		return nil, err
	}
	if allocate && !c.IsMain && st.Profile != profile {
		if err := st.SetProfile(ctx, profile); err != nil {
			return nil, err
		}
	}
	c.Profile = profile
	v["profile"] = profile
	c.Phases, c.RemoveDeclared = effective(e.cfg, profile)

	if err := e.resolveUserVars(ctx, c, allocate); err != nil {
		return nil, err
	}
	sort.Strings(c.Pending)
	return c, nil
}

// chooseProfile: a created worktree keeps its profile; otherwise the
// explicit profile (flag or TOSK_PROFILE, read by the caller), then the
// first matching rule of the config in file order.
func (e *Engine) chooseProfile(c *Target) (string, error) {
	if c.IsMain {
		return "", nil
	}
	if c.State.Created {
		return c.State.Profile, nil
	}
	name := e.profile
	if name == "" {
		for _, rule := range e.cfg.ProfileRules {
			ok, err := expr.Eval(rule.When, c.exprEnv())
			if err != nil {
				return "", fmt.Errorf("profile rule %s: %w", rule.Name, err)
			}
			if ok {
				name = rule.Name
				break
			}
		}
	}
	if name != "" {
		if _, ok := e.cfg.Profiles[name]; !ok {
			return "", fmt.Errorf("unknown profile %q", name)
		}
	}
	return name, nil
}

func (c *Target) exprEnv() expr.Env {
	return expr.Env{
		Vars: c.Vars,
		Exists: func(p string) bool {
			name, err := local(p)
			if err != nil {
				return false
			}
			_, err = os.Lstat(filepath.Join(c.WT.Path, filepath.FromSlash(name)))
			return err == nil
		},
	}
}

// effective merges a profile into the base phases: `skip` drops base
// steps, a step with the same name replaces the base one, others are
// appended.
func effective(cfg *config.Config, profile string) (config.Phases, bool) {
	prof := cfg.Profiles[profile]
	p := config.Phases{
		Create: merge(cfg.Create, prof.Create),
		Sync:   merge(cfg.Sync, prof.Sync),
		Remove: merge(cfg.Remove, prof.Remove),
	}
	return p, cfg.Remove != nil || prof.Remove != nil
}

func merge(base, prof []config.Step) []config.Step {
	skip := map[string]bool{}
	for _, s := range prof {
		for _, name := range s.Skip {
			skip[name] = true
		}
	}
	var out []config.Step
	for _, s := range base {
		if s.Kind != config.Skip && !skip[s.Name()] {
			out = append(out, s)
		}
	}
next:
	for _, s := range prof {
		if s.Kind == config.Skip {
			continue
		}
		for i := range out {
			if out[i].Name() == s.Name() {
				out[i] = s
				continue next
			}
		}
		out = append(out, s)
	}
	return out
}

// resolveUserVars resolves the config's variables. They may reference each
// other, so it loops until nothing more can be resolved.
func (e *Engine) resolveUserVars(ctx context.Context, c *Target, allocate bool) error {
	todo := map[string]config.Var{}
	for name, def := range e.cfg.Vars {
		todo[name] = def
	}
	names := func() []string {
		list := make([]string, 0, len(todo))
		for n := range todo {
			list = append(list, n)
		}
		sort.Strings(list)
		return list
	}
	for progressed := true; progressed && len(todo) > 0; {
		progressed = false
		for _, name := range names() {
			def := todo[name]
			var val string
			switch {
			case !def.IsProvider():
				expanded, err := c.Vars.Expand(def.Value)
				if err != nil {
					continue // depends on a variable not resolved yet
				}
				val = expanded
			case hasKey(c.State.Vars, name):
				val = c.State.Vars[name]
			case allocate:
				allocated, err := e.allocate(ctx, c, name, def)
				if err != nil {
					return fmt.Errorf("variable %s: %w", name, err)
				}
				if err := c.State.SetVar(ctx, name, allocated); err != nil {
					return err
				}
				val = allocated
			default:
				continue
			}
			c.Vars[name] = val
			delete(todo, name)
			progressed = true
		}
	}
	for _, name := range names() {
		def := todo[name]
		if !def.IsProvider() && !refersTo(def.Value, todo) {
			_, err := c.Vars.Expand(def.Value)
			return fmt.Errorf("variable %s: %w", name, err)
		}
		if allocate {
			return fmt.Errorf("variable %s: circular reference", name)
		}
		c.Pending = append(c.Pending, name)
	}
	return nil
}

func hasKey(m map[string]string, k string) bool { _, ok := m[k]; return ok }

// refersTo reports whether s references one of the unresolved variables
// other than through a typo.
func refersTo(s string, todo map[string]config.Var) bool {
	for _, ref := range vars.Refs(s) {
		if _, ok := todo[ref]; ok {
			return true
		}
	}
	return false
}

// UserVars returns the config's variables that are resolved, as
// "name=value" in name order.
func (e *Engine) UserVars(c *Target) []string {
	var list []string
	for _, name := range c.Vars.Names() {
		if _, ok := e.cfg.Vars[name]; ok {
			list = append(list, name+"="+c.Vars[name])
		}
	}
	return list
}

// resolveAll resolves the live worktrees under the allocation lock, making
// the missing allocations. With only set, just that worktree.
func (e *Engine) resolveAll(ctx context.Context, only string) ([]*Target, error) {
	if err := state.Enable(ctx, e.main.Path); err != nil {
		return nil, err
	}
	unlock, err := e.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	var list []*Target
	for _, wt := range e.all {
		if wt.Bare || wt.Prunable || (only != "" && wt.Path != only) {
			continue
		}
		c, err := e.resolve(ctx, wt, true)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(wt.Path), err)
		}
		list = append(list, c)
	}
	return list, nil
}

// SyncAll reconciles every worktree with the config: the create phase for
// those not created yet, then the sync phase. The main worktree only gets
// its variables.
func (e *Engine) SyncAll(ctx context.Context) error { return e.sync(ctx, "") }

// SyncOne reconciles a single worktree, as SyncAll does for all of them.
func (e *Engine) SyncOne(ctx context.Context, wt git.Worktree) error { return e.sync(ctx, wt.Path) }

func (e *Engine) sync(ctx context.Context, only string) error {
	targets, err := e.resolveAll(ctx, only)
	if err != nil {
		return err
	}
	linked := 0
	for _, c := range targets {
		if !c.IsMain {
			linked++
		}
	}
	failed := 0
	for _, c := range targets {
		if c.IsMain {
			continue
		}
		if linked > 1 {
			c.header = filepath.Base(c.WT.Path)
		}
		if err := e.syncTarget(ctx, c); err != nil {
			// Interrupted: stop here rather than fail each remaining
			// worktree in turn.
			if ctx.Err() != nil {
				return fmt.Errorf("interrupted while preparing %s: %w", filepath.Base(c.WT.Path), ctx.Err())
			}
			fmt.Fprintf(e.err, "tosk: error [%s] %v\n", filepath.Base(c.WT.Path), err)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("sync failed for %d worktree(s)", failed)
	}
	return nil
}

func (e *Engine) syncTarget(ctx context.Context, c *Target) error {
	start := time.Now()
	creating := !c.State.Created
	if creating {
		if uv := e.UserVars(c); len(uv) > 0 {
			c.say("vars", "%s", strings.Join(uv, " "))
		}
		for _, s := range c.Phases.Create {
			if err := e.runStep(ctx, c, "create", s, false); err != nil {
				return fmt.Errorf("%s: %w", s.Name(), err)
			}
		}
		if err := e.event(ctx, c, "tosk-post-create"); err != nil {
			return err
		}
		if err := c.State.SetCreated(ctx); err != nil {
			return err
		}
	}
	for _, s := range c.Phases.Sync {
		if err := e.runStep(ctx, c, "sync", s, false); err != nil {
			return fmt.Errorf("%s: %w", s.Name(), err)
		}
	}
	if creating {
		c.say("ready", "in %s", time.Since(start).Round(100*time.Millisecond))
	}
	return nil
}

// Remove runs the remove phase of a worktree and releases what it holds.
// It does not delete the directory. With force, failing steps are reported
// and skipped instead of aborting.
func (e *Engine) Remove(ctx context.Context, wt git.Worktree, force bool) error {
	c, err := e.Inspect(ctx, wt)
	if err != nil {
		return err
	}
	fail := func(what string, err error) error {
		if force {
			fmt.Fprintf(e.err, "tosk: error %s: %v (ignored)\n", what, err)
			return nil
		}
		return fmt.Errorf("%s: %w (use --force to remove anyway)", what, err)
	}
	for _, s := range c.Phases.Remove {
		if err := e.runStep(ctx, c, "remove", s, true); err != nil {
			if err := fail(s.Name(), err); err != nil {
				return err
			}
		}
	}
	if err := e.event(ctx, c, "tosk-pre-remove"); err != nil {
		if err := fail("tosk-pre-remove", err); err != nil {
			return err
		}
	}
	if err := e.release(ctx, c); err != nil {
		if err := fail("release", err); err != nil {
			return err
		}
	}
	// Links to the main worktree go first, so that deleting the directory
	// can never follow one into the main worktree (junctions on Windows).
	// A link that cannot be removed stops the removal for that reason.
	root, err := os.OpenRoot(wt.Path)
	if err != nil {
		return fail("unlink", err)
	}
	defer root.Close()
	steps := append(append([]config.Step{}, c.Phases.Create...), c.Phases.Sync...)
	for i := len(steps) - 1; i >= 0; i-- {
		if steps[i].Kind != config.Link {
			continue
		}
		expanded, err := c.Vars.Expand(steps[i].Arg)
		if err != nil {
			continue
		}
		name, err := local(expanded)
		if err != nil {
			continue
		}
		info, err := root.Lstat(name)
		if err != nil || !isLinkMode(info) {
			continue
		}
		if err := root.Remove(name); err != nil {
			if err := fail("unlink "+name, err); err != nil {
				return err
			}
			continue
		}
		c.say("unlink", "%s", name)
	}
	return nil
}

// RunStep executes one step of the worktree by name, whatever its phase,
// `when` condition or `once` record.
func (e *Engine) RunStep(ctx context.Context, wt git.Worktree, name string) error {
	targets, err := e.resolveAll(ctx, wt.Path)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return errors.New("worktree is not usable")
	}
	c := targets[0]
	// Phases in lifecycle order: a name present in several runs in the
	// first.
	phases := []struct {
		name  string
		steps []config.Step
	}{{"create", c.Phases.Create}, {"sync", c.Phases.Sync}, {"remove", c.Phases.Remove}}
	seen := map[string]bool{}
	var known []string
	for _, phase := range phases {
		for _, s := range phase.steps {
			if s.Name() == name {
				return e.runStep(ctx, c, phase.name, s, true)
			}
			if !seen[s.Name()] {
				seen[s.Name()] = true
				known = append(known, s.Name())
			}
		}
	}
	sort.Strings(known)
	return fmt.Errorf("no step %q; steps are:\n  %s", name, strings.Join(known, "\n  "))
}
