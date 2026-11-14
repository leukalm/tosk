package expr

import "testing"

func TestEval(t *testing.T) {
	env := Env{
		Vars:   map[string]string{"branch": "agent/fix-login", "profile": "agent", "empty": ""},
		Exists: func(p string) bool { return p == "data/seed.sql" },
	}
	cases := map[string]bool{
		"":                                       true,
		"exists('data/seed.sql')":                true,
		"exists('nope')":                         false,
		"!exists('nope')":                        true,
		"branch matches '^agent/'":               true,
		"branch matches '^feat/'":                false,
		"profile == 'agent'":                     true,
		`profile != "agent"`:                     false,
		"profile == 'x' || exists('nope')":       false,
		"profile == 'x' || branch matches 'fix'": true,
		"profile == 'agent' && !exists('data/seed.sql')":          false,
		"!(profile == 'x') && (exists('data/seed.sql') || empty)": true,
		"empty":   false,
		"profile": true,
	}
	for src, want := range cases {
		got, err := Eval(src, env)
		if err != nil {
			t.Errorf("%q: %v", src, err)
		} else if got != want {
			t.Errorf("%q = %v, want %v", src, got, want)
		}
	}
}

func TestEvalErrors(t *testing.T) {
	for _, src := range []string{"nope == 'x'", "profile ==", "'a' 'b'", "(profile", "foo('x')", "branch matches '('", "'open"} {
		if _, err := Eval(src, Env{Vars: map[string]string{"profile": "", "branch": ""}}); err == nil {
			t.Errorf("%q: expected an error", src)
		}
	}
}
