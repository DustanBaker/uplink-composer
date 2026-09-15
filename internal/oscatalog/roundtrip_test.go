package oscatalog

import (
	"context"
	"strings"
	"testing"

	"github.com/uplinkresearch/dsky/internal/library"
	"github.com/uplinkresearch/dsky/internal/workspace"
)

// Editing a recipe must not quietly drop a program. A recipe of Dusty's came
// back from an edit with eight programs where it had nine: Discord, which is
// per-user and so the odd one out, was gone. Nothing found since reproduces
// it -- the form, the dialog and the saved file all keep every program -- so
// this test stands guard over the round trip that would show it.
func TestEditingARecipeKeepsEveryProgram(t *testing.T) {
	lib, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wsDir := t.TempDir() + "/ws"
	if err := workspace.ScaffoldEmpty(wsDir, "Acme"); err != nil {
		t.Fatal(err)
	}
	win, ok := Get("windows-11")
	if !ok {
		t.Skip("no windows-11 in the catalog")
	}
	// The mix that matters: machine-wide packages, a package that only ever
	// installs for one account, and one typed in by hand.
	apps := []string{"chrome", "7zip", "teams", "vlc", "spotify", "discord", "winget:Some.Tool"}
	opts := Options{Edition: "Pro", AccountMode: "local", Debloat: "standard", Apps: apps}
	if _, err := SaveRecipe(context.Background(), lib, wsDir, "hp", "hp", win, opts, nil); err != nil {
		t.Fatal(err)
	}

	load := func() []string {
		t.Helper()
		ws, err := workspace.Load(wsDir)
		if err != nil {
			t.Fatal(err)
		}
		r, err := ws.Recipe("hp")
		if err != nil {
			t.Fatal(err)
		}
		return r.Windows.Apps.Winget
	}
	first := load()
	if len(first) != len(apps) {
		t.Fatalf("saving wrote %d packages for %d programs: %v", len(first), len(apps), first)
	}

	// Two rounds, because a drop could take an edit to appear.
	for round := 1; round <= 2; round++ {
		ws, _ := workspace.Load(wsDir)
		r, _ := ws.Recipe("hp")
		form, err := FormFromRecipe(wsDir, r)
		if err != nil {
			t.Fatalf("round %d: the recipe is no longer editable: %v", round, err)
		}
		if len(form.Apps) != len(apps) {
			t.Fatalf("round %d: the dialog would offer %d programs, not %d: %v", round, len(form.Apps), len(apps), form.Apps)
		}
		next := Options{Edition: form.Edition, AccountMode: form.AccountMode, Debloat: form.Debloat,
			BypassRequirement: form.BypassRequirement, Apps: form.Apps, Hardware: form.Hardware}
		if _, err := ReplaceRecipe(context.Background(), lib, wsDir, "hp", "hp", win, next, nil); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if got := load(); strings.Join(got, ",") != strings.Join(first, ",") {
			t.Fatalf("round %d changed the program list:\n before %v\n after  %v", round, first, got)
		}
	}
}
