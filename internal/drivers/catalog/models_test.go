package catalog

import (
	"slices"
	"strings"
	"testing"
)

// Listing models is what the Install dialog's model picker shows: every model
// the catalog has a pack for, for this OS, each once, sorted.
func TestListModels(t *testing.T) {
	lenovo, err := listLenovo([]byte(lenovoFixture), "win11")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(lenovo, []string{"ThinkCentre M70q Gen 3", "ThinkPad T14 Gen 4"}) {
		t.Errorf("lenovo: %q", lenovo)
	}

	// Dell lists a model once even though three packages name it, and a pack
	// for another OS does not add it again.
	dell, err := listDell([]byte(dellFixture), "win11", "x64")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(dell, []string{"OptiPlex 7010"}) {
		t.Errorf("dell: %q", dell)
	}
	two := strings.Replace(dellFixture, `<Model systemID="0B12" name="OptiPlex 7010"/></Brand></SupportedSystems>
 <Cryptography><Hash algorithm="SHA256">CC01`, `<Model systemID="0B12" name="OptiPlex 7010"/><Model systemID="0C99" name="OptiPlex 7010 Plus"/></Brand></SupportedSystems>
 <Cryptography><Hash algorithm="SHA256">CC01`, 1)
	if dell, _ = listDell([]byte(two), "win11", "x64"); !slices.Equal(dell, []string{"OptiPlex 7010", "OptiPlex 7010 Plus"}) {
		t.Errorf("dell, a pack naming two models: %q", dell)
	}
	if none, _ := listDell([]byte(dellFixture), "win11", "arm64"); len(none) != 0 {
		t.Errorf("dell arm64: %q", none)
	}

	hp, err := listHP([]byte(hpFixture), "win11", "x64")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hp, []string{"HP EliteDesk 800 G9 Desktop Mini PC"}) {
		t.Errorf("hp: %q", hp)
	}
}

// Picking "OptiPlex 7010" from the list must get the 7010's pack, not the
// newer pack for a model whose name merely contains it.
func TestExactModelWinsOverNewerLongerName(t *testing.T) {
	packs := []Pack{
		{Model: "OptiPlex 7010 Plus", Released: "2025-06-01"},
		{Model: "OptiPlex 7010", Released: "2025-03-10"},
	}
	if got := Exact(packs, "optiplex  7010"); got.Model != "OptiPlex 7010" {
		t.Errorf("exact pick: %q", got.Model)
	}
	if got := Exact(packs, "OptiPlex"); got.Model != "OptiPlex 7010 Plus" {
		t.Errorf("no exact name falls back to newest: %q", got.Model)
	}
}
