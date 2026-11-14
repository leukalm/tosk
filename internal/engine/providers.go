package engine

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"

	"github.com/leukalm/tosk/internal/config"
)

// Built-in providers. Anything else is a script, called as
//
//	<script> allocate <name>          prints the value on stdout
//	<script> release <name> <value>   on `tosk rm` (value also in TOSK_VALUE)
//
// in the worktree, with the variables resolved so far as TOSK_<NAME>.
const (
	providerPort    = "port"
	providerCounter = "counter"
)

func (e *Engine) allocate(ctx context.Context, c *Target, name string, def config.Var) (string, error) {
	switch def.Provider {
	case providerPort:
		return e.allocatePort(ctx, c, def)
	case providerCounter:
		return e.allocateCounter(ctx, c, name, def)
	}
	cmd, err := providerCmd(ctx, c, def, "allocate "+name)
	if err != nil {
		return "", err
	}
	cmd.Stderr = e.err
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("provider %s: %w", def.Provider, err)
	}
	val := strings.TrimSpace(string(out))
	if val == "" {
		return "", fmt.Errorf("provider %s printed nothing", def.Provider)
	}
	return val, nil
}

// allocatePort picks the first port of [base, base+range) that no worktree
// holds and that nothing listens on.
func (e *Engine) allocatePort(ctx context.Context, c *Target, def config.Var) (string, error) {
	base, err := intOpt(def, "base", 0)
	if err != nil {
		return "", err
	}
	size, err := intOpt(def, "range", 100)
	if err != nil {
		return "", err
	}
	if base <= 0 || size <= 0 || base+size-1 > 65535 {
		return "", fmt.Errorf("port provider needs a valid `base` and `range`")
	}
	// A port held by any port variable of any worktree is taken, this
	// worktree's other port variables included.
	others, err := e.others(ctx, c.WT)
	if err != nil {
		return "", err
	}
	used := map[string]bool{}
	for name, d := range e.cfg.Vars {
		if d.Provider != providerPort {
			continue
		}
		for _, o := range append(others, c.State) {
			if v, ok := o.Vars[name]; ok {
				used[v] = true
			}
		}
	}
	for p := base; p < base+size; p++ {
		s := strconv.Itoa(p)
		if !used[s] && portFree(ctx, p) {
			return s, nil
		}
	}
	return "", fmt.Errorf("no free port in %d-%d", base, base+size-1)
}

func portFree(ctx context.Context, port int) bool {
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// allocateCounter picks the smallest number >= start (default 1) that no
// other worktree holds for this variable.
func (e *Engine) allocateCounter(ctx context.Context, c *Target, name string, def config.Var) (string, error) {
	n, err := intOpt(def, "start", 1)
	if err != nil {
		return "", err
	}
	others, err := e.others(ctx, c.WT)
	if err != nil {
		return "", err
	}
	used := map[string]bool{}
	for _, o := range others {
		if v, ok := o.Vars[name]; ok {
			used[v] = true
		}
	}
	for used[strconv.Itoa(n)] {
		n++
	}
	return strconv.Itoa(n), nil
}

func intOpt(def config.Var, key string, fallback int) (int, error) {
	switch v := def.Options[key].(type) {
	case nil:
		return fallback, nil
	case int:
		return v, nil
	case string:
		return strconv.Atoi(v)
	}
	return 0, fmt.Errorf("option %s must be a number", key)
}

// release tells script providers that the worktree's values are free
// again. Built-in providers need nothing: their record is the worktree's
// own config, which disappears with it.
func (e *Engine) release(ctx context.Context, c *Target) error {
	for _, name := range c.Vars.Names() {
		def, ok := e.cfg.Vars[name]
		if !ok || !def.IsProvider() || def.Provider == providerPort || def.Provider == providerCounter {
			continue
		}
		val, ok := c.State.Vars[name]
		if !ok {
			continue
		}
		// The value goes through the environment, never into the
		// command line.
		cmd, err := providerCmd(ctx, c, def, "release "+name+` "$TOSK_VALUE"`)
		if err != nil {
			return err
		}
		cmd.Env = append(cmd.Env, "TOSK_VALUE="+val)
		cmd.Stdout, cmd.Stderr = e.out, e.err
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("provider %s: %w", def.Provider, err)
		}
	}
	return nil
}

// providerCmd builds the call to a script provider. Variable names are
// identifiers (checked by config.Parse), so args is safe shell source.
func providerCmd(ctx context.Context, c *Target, def config.Var, args string) (*exec.Cmd, error) {
	script, err := c.Vars.ShellExpand(def.Provider)
	if err != nil {
		return nil, err
	}
	return shell(ctx, script+" "+args, c)
}
