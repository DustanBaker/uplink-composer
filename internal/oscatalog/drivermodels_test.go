package oscatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uplinkresearch/dsky/internal/driverresolve"
	"github.com/uplinkresearch/dsky/internal/library"
	"github.com/uplinkresearch/dsky/internal/recipe"
	"github.com/uplinkresearch/dsky/internal/workspace"
)

// A recipe saved for a machine somebody named must come back with that machine
// in it. Dusty picked "HP EliteBook x360 1040 G8" in the dialog and the saved
// recipe had no drivers at all -- because the pack it resolves to is fetched
// during the save, and anything that could not be resolved right then was
// dropped along with the machine that asked for it, with no error anywhere.
func TestANamedModelIsNeverQuietlyDropped(t *testing.T) {
	yaml := recipeYAML(recipeMeta{ID: "hp-bench", Name: "HP bench"}, Entry{ID: "windows-11", Family: Windows},
		Options{Edition: "Pro", AccountMode: "local", Debloat: "standard"},
		[]recipe.HardwareSpec{{Vendor: "hp", Model: "HP EliteBook x360 1040 G8 Notebook PC", OS: "win11"}})
	if !strings.Contains(yaml, "hardware:") || !strings.Contains(yaml, "EliteBook x360 1040 G8") {
		t.Fatalf("the model is not in the saved recipe:\n%s", yaml)
	}
}

// The two kinds of driver request are kept apart, because they are treated
// differently when a pack cannot be found: detected hardware is best-effort,
// a named machine is not.
func TestDetectedAndNamedDriversAreSeparate(t *testing.T) {
	var o Options
	o.Hardware = []recipe.HardwareSpec{{Vendor: "dell", Model: "OptiPlex 3070", OS: "win11"}}
	o.Models = []recipe.HardwareSpec{{Vendor: "hp", Model: "EliteBook 840 G8", OS: "win11"}}
	o.Kept = []recipe.HardwareSpec{{HWIDs: []string{"PCI\\VEN_8086"}, OS: "win11"}}
	if len(o.Hardware) != 1 || len(o.Models) != 1 || len(o.Kept) != 1 {
		t.Fatal("the fields are not independent")
	}
}

// Keeping a recipe's drivers and picking the same model again must not stage
// it twice.
func TestTheSameModelIsNotStagedTwice(t *testing.T) {
	hw := []recipe.HardwareSpec{
		{Vendor: "hp", Model: "EliteBook 840 G8", OS: "win11"},
		{Vendor: "HP", Model: "EliteBook 840 G8", OS: "win11"},
		{Vendor: "dell", Model: "OptiPlex 3070", OS: "win11"},
		{HWIDs: []string{"PCI\\VEN_8086&DEV_1234"}, OS: "win11"},
		{HWIDs: []string{"PCI\\VEN_8086&DEV_1234"}, OS: "win11"},
	}
	got := dedupeSpecs(append([]recipe.HardwareSpec{}, hw...))
	if len(got) != 3 {
		t.Errorf("deduped to %d entries, want 3: %+v", len(got), got)
	}
}

// Saving a recipe must not fetch a vendor's driver pack. Naming an HP
// EliteBook meant waiting on 1.2 GB before the recipe file existed -- and if
// that download was interrupted, or the dialog was closed, there was no recipe
// at all. The manifest carries the URL and the SHA-256; the bytes are fetched
// when a stick is built.
func TestSavingARecipeFetchesNothing(t *testing.T) {
	var fetched []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetched = append(fetched, r.URL.Path)
		w.Write([]byte("pack bytes"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	lib, err := library.Open(filepath.Join(dir, "lib"))
	if err != nil {
		t.Fatal(err)
	}
	wsDir := filepath.Join(dir, "ws")
	if err := os.MkdirAll(filepath.Join(wsDir, "manifests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "workspace.yaml"), []byte("version: 1\norg:\n  name: Test\n  id: test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A manifest for the model, as a previous save would have left it: the
	// pack is described but its bytes are not in the library.
	man := "id: hp-pack\nkind: driver-pack\nformat: zip\nurl: " + srv.URL + "/hp.zip\n" +
		"sha256: 0000000000000000000000000000000000000000000000000000000000000000\n" +
		"filename: hp.zip\nhardware:\n  vendor: hp\n  model: \"EliteBook x360 1040 G8\"\n  os: win11\n"
	if err := os.WriteFile(filepath.Join(wsDir, "manifests", "hp-pack.yaml"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := workspace.Load(wsDir)
	if err != nil {
		t.Fatal(err)
	}
	hw := []recipe.HardwareSpec{{Vendor: "hp", Model: "EliteBook x360 1040 G8", OS: "win11"}}
	res, err := driverresolve.Plan(context.Background(), ws, lib, hw, true, nil)
	if err != nil {
		t.Fatalf("planning refused: %v", err)
	}
	if len(fetched) != 0 {
		t.Errorf("saving fetched %v; it should fetch nothing", fetched)
	}
	if len(res.Specs) != 1 || res.Specs[0].Model != "EliteBook x360 1040 G8" {
		t.Errorf("the model did not survive planning: %+v", res.Specs)
	}
}
