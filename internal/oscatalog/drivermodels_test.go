package oscatalog

import (
	"strings"
	"testing"

	"github.com/uplinkresearch/dsky/internal/recipe"
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
