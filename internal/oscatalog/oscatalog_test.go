package oscatalog

import (
	"testing"

	"github.com/DustanBaker/uplink-composer/internal/library"
	"github.com/DustanBaker/uplink-composer/internal/recipe"
	"github.com/DustanBaker/uplink-composer/internal/workspace"
)

// TestSynthesizedRecipesValid builds the ephemeral workspace for every
// catalog entry and confirms the generated recipe + manifest load and
// validate — a broken template or YAML would break Quick Install silently.
func TestSynthesizedRecipesValid(t *testing.T) {
	lib, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		opts Options
	}{
		{"win-local", Options{Edition: "Pro", AccountMode: "local", Debloat: "standard"}},
		{"win-oobe-bypass", Options{Edition: "Home", AccountMode: "oobe", Debloat: "off", BypassRequirement: true}},
	}
	for _, e := range Catalog() {
		if e.Family != Windows {
			dir, err := scaffoldQuickWorkspace(lib, e, Options{})
			if err != nil {
				t.Fatalf("%s: scaffold: %v", e.ID, err)
			}
			assertLoads(t, dir, e.ID)
			continue
		}
		for _, c := range cases {
			dir, err := scaffoldQuickWorkspace(lib, e, c.opts)
			if err != nil {
				t.Fatalf("%s/%s: scaffold: %v", e.ID, c.name, err)
			}
			r := assertLoads(t, dir, e.ID)
			if r == nil {
				continue
			}
			if r.Windows == nil || r.Windows.EICfg == nil {
				t.Errorf("%s/%s: missing windows/ei_cfg", e.ID, c.name)
			}
			for _, f := range r.Lint() {
				if f.Severity == "error" {
					t.Errorf("%s/%s: lint error: %s", e.ID, c.name, f.Message)
				}
			}
		}
	}
}

func assertLoads(t *testing.T, dir, id string) *recipe.Recipe {
	t.Helper()
	ws, err := workspace.Load(dir)
	if err != nil {
		t.Fatalf("load workspace %s: %v", dir, err)
	}
	r, err := ws.Recipe(id)
	if err != nil {
		t.Errorf("recipe %s did not load/validate: %v", id, err)
		return nil
	}
	if _, err := ws.Source(id); err != nil {
		t.Errorf("manifest for %s missing: %v", id, err)
	}
	return r
}

// TestGenericKeysComplete makes sure every Windows edition option has a key
// and an ei.cfg name.
func TestGenericKeysComplete(t *testing.T) {
	for _, e := range Catalog() {
		if e.Family != Windows {
			continue
		}
		for _, ed := range e.Editions {
			if genericKeys[ed] == "" {
				t.Errorf("%s: no generic key for edition %q", e.ID, ed)
			}
			if editionName(ed) == "" {
				t.Errorf("%s: no ei.cfg name for edition %q", e.ID, ed)
			}
		}
	}
}
