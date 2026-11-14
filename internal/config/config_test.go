package config

import (
	"os"
	"strings"
	"testing"
)

// The example from the README must always parse.
func TestParseReadmeExample(t *testing.T) {
	data, err := os.ReadFile("../../testdata/readme.tosk.yml")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}

	if p := c.Vars["port"]; p.Provider != "port" || p.Options["base"] != 8100 || p.Options["range"] != 100 {
		t.Errorf("port var = %+v", p)
	}
	if db := c.Vars["db"]; db.IsProvider() || db.Value != "myapp_{{slug}}" {
		t.Errorf("db var = %+v", db)
	}

	var names []string
	for _, s := range c.Create {
		names = append(names, s.Name())
	}
	want := "copy:.env copy:config/*.local.properties link:node_modules " +
		"template:config/application-local.properties.tpl replace:.env write:.tosk-env " +
		"run:./scripts/create-db.sh {{db}} run:mvn -q dependency:go-offline run:./scripts/seed.sh"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("create steps:\n got %s\nwant %s", got, want)
	}

	seed := c.Create[8]
	if seed.When != "exists('data/seed.sql')" || seed.Mode != ModeOnce {
		t.Errorf("seed step = %+v", seed)
	}
	if m := c.Sync[0]; m.Arg != "./scripts/migrate.sh {{db}}" || m.Mode != ModeAlways {
		t.Errorf("sync step = %+v", m)
	}
	if len(c.Remove) != 2 {
		t.Errorf("remove has %d steps", len(c.Remove))
	}

	agent := c.Profiles["agent"].Create
	if len(agent) != 2 || agent[0].Kind != Skip || agent[0].Skip[0] != "link:node_modules" {
		t.Errorf("agent profile = %+v", agent)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"unknown step":    "create:\n  - frobnicate: x\n",
		"unknown key":     "bogus: 1\n",
		"bad mode":        "sync:\n  - run: { cmd: x, mode: sometimes }\n",
		"two kinds":       "create:\n  - copy: a\n    link: b\n",
		"replace no file": "create:\n  - replace: { match: x }\n",
		"var no provider": "vars:\n  port: { base: 1 }\n",
	}
	for name, in := range cases {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestParseEmpty(t *testing.T) {
	if _, err := Parse(nil); err != nil {
		t.Fatal(err)
	}
}

func TestWhenAtStepLevel(t *testing.T) {
	c, err := Parse([]byte("create:\n  - copy: .env\n    when: \"profile == 'agent'\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Create[0].When != "profile == 'agent'" {
		t.Errorf("when = %q", c.Create[0].When)
	}
}

// Rules are tried in file order: the first match wins, so "zeta" listed
// before "alpha" must stay first.
func TestProfileRulesKeepFileOrder(t *testing.T) {
	c, err := Parse([]byte("profile:\n  zeta: \"branch == 'x'\"\n  alpha: \"branch == 'y'\"\n  mid: \"true\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range c.ProfileRules {
		names = append(names, r.Name)
	}
	if got := strings.Join(names, " "); got != "zeta alpha mid" {
		t.Errorf("rules = %s", got)
	}
	if c.ProfileRules[1].When != "branch == 'y'" {
		t.Errorf("when = %q", c.ProfileRules[1].When)
	}
	for name, in := range map[string]string{
		"list":       "profile: [a]\n",
		"non-scalar": "profile:\n  a: { b: c }\n",
		"duplicate":  "profile:\n  a: x\n  a: y\n",
	} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
