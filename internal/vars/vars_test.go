package vars

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"feat/invoices":     "feat_invoices",
		"Feat/Invoices-v2":  "feat_invoices_v2",
		"agent/fix--thing/": "agent_fix_thing",
		"main":              "main",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuiltin(t *testing.T) {
	path := filepath.Join("work", "app-feat-invoices")
	v := Builtin(path, "feat/invoices", "app")
	want := Vars{"name": "app-feat-invoices", "slug": "feat_invoices", "branch": "feat/invoices", "path": path, "main": "app"}
	if !reflect.DeepEqual(v, want) {
		t.Errorf("got %v", v)
	}
	if d := Builtin(path, "", "app"); d["slug"] != "app_feat_invoices" {
		t.Errorf("detached slug = %q", d["slug"])
	}
}

func TestExpand(t *testing.T) {
	v := Vars{"slug": "feat_invoices", "port": "8103"}
	got, err := v.Expand("myapp_{{slug}}:{{ port }}")
	if err != nil || got != "myapp_feat_invoices:8103" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := v.Expand("{{nope}}"); err == nil {
		t.Error("expected an error for an unknown variable")
	}
}

func TestEnv(t *testing.T) {
	got := Vars{"port": "1", "db": "x"}.Env()
	if !reflect.DeepEqual(got, []string{"TOSK_DB=x", "TOSK_PORT=1"}) {
		t.Errorf("got %v", got)
	}
}
