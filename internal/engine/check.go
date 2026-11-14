package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/leukalm/tosk/internal/state"
)

// Severity of a Finding.
type Severity int

const (
	// Warning is worth knowing but not a failure.
	Warning Severity = iota
	// Problem makes `tosk doctor` fail.
	Problem
)

// FindingKind says what a Finding is about.
type FindingKind int

const (
	// Orphaned: the worktree's directory is gone but git still lists it.
	Orphaned FindingKind = iota
	// Unreadable: the worktree's Tosk record cannot be read.
	Unreadable
	// Duplicate: a provider handed the same value to several worktrees.
	Duplicate
	// Unprepared: worktrees that `tosk sync` has not prepared yet.
	Unprepared
)

// Finding is one result of Check.
type Finding struct {
	Severity Severity
	Kind     FindingKind
	Message  string
}

// Check inspects the worktrees' records, read-only, and reports what looks
// wrong: orphaned or unreadable records, values that should be unique but
// are held twice, worktrees not prepared yet. The result is in a stable
// order.
func (e *Engine) Check(ctx context.Context) []Finding {
	var findings []Finding
	held := map[string][]string{} // "<provider> <value>" -> "<worktree>:<var>"
	pending := 0

	varNames := make([]string, 0, len(e.cfg.Vars))
	for name, def := range e.cfg.Vars {
		if def.IsProvider() {
			varNames = append(varNames, name)
		}
	}
	sort.Strings(varNames)

	for i, wt := range e.all {
		name := filepath.Base(wt.Path)
		if wt.Prunable {
			findings = append(findings, Finding{Problem, Orphaned,
				fmt.Sprintf("%s: directory is gone, its allocations are orphaned (run `git worktree prune`)", name)})
			continue
		}
		if wt.Bare {
			continue
		}
		st, err := state.Load(ctx, wt.Path)
		if err != nil {
			findings = append(findings, Finding{Problem, Unreadable, fmt.Sprintf("%s: %v", name, err)})
			continue
		}
		if i > 0 && !st.Created {
			pending++
		}
		for _, v := range varNames {
			if val, ok := st.Vars[v]; ok {
				key := e.cfg.Vars[v].Provider + " " + val
				held[key] = append(held[key], name+":"+v)
			}
		}
	}

	keys := make([]string, 0, len(held))
	for key := range held {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if owners := held[key]; len(owners) > 1 {
			findings = append(findings, Finding{Problem, Duplicate,
				fmt.Sprintf("%s is allocated more than once: %s", key, strings.Join(owners, ", "))})
		}
	}
	if pending > 0 {
		findings = append(findings, Finding{Warning, Unprepared,
			fmt.Sprintf("%d worktree(s) not prepared yet (run `tosk sync`)", pending)})
	}
	return findings
}
