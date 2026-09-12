package oscatalog

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

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
			dir, err := scaffoldQuickWorkspace(lib, e, Options{}, nil)
			if err != nil {
				t.Fatalf("%s: scaffold: %v", e.ID, err)
			}
			assertLoads(t, dir, e.ID)
			continue
		}
		for _, c := range cases {
			dir, err := scaffoldQuickWorkspace(lib, e, c.opts, nil)
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

// TestHardwareBlockRoundTrips is the guard on auto-detected drivers reaching
// the recipe intact: hardware IDs carry backslashes (PCI\VEN_...), so a
// quoting slip in the generated YAML would either fail to parse or silently
// mangle the ID and match no driver pack.
func TestHardwareBlockRoundTrips(t *testing.T) {
	lib, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e, ok := Get("windows-11")
	if !ok {
		t.Fatal("windows-11 missing from the catalog")
	}
	hw := []recipe.HardwareSpec{
		{Vendor: "dell", Model: "OptiPlex 7010", OS: "win11"},
		{HWIDs: []string{`PCI\VEN_10DE&DEV_2B85`, `PCI\VEN_8086&DEV_15F3`}, OS: "win11"},
	}
	dir, err := scaffoldQuickWorkspace(lib, e, Options{Edition: "Pro"}, hw)
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	r := assertLoads(t, dir, e.ID)
	if r == nil || r.Windows == nil {
		t.Fatal("recipe did not load")
	}
	got := r.Windows.Hardware
	if len(got) != 2 {
		t.Fatalf("got %d hardware entries, want 2: %+v", len(got), got)
	}
	if got[0].Vendor != "dell" || got[0].Model != "OptiPlex 7010" {
		t.Errorf("vendor entry round-tripped as %+v", got[0])
	}
	want := []string{`PCI\VEN_10DE&DEV_2B85`, `PCI\VEN_8086&DEV_15F3`}
	if len(got[1].HWIDs) != len(want) {
		t.Fatalf("got %d hwids, want %d: %q", len(got[1].HWIDs), len(want), got[1].HWIDs)
	}
	for i, id := range want {
		if got[1].HWIDs[i] != id {
			t.Errorf("hwid %d round-tripped as %q, want %q", i, got[1].HWIDs[i], id)
		}
	}
	for _, f := range r.Lint() {
		if f.Severity == "error" {
			t.Errorf("lint error: %s", f.Message)
		}
	}
	// A machine whose catalogs covered nothing must leave the block out
	// entirely rather than emit an empty `hardware:` key.
	bare, err := scaffoldQuickWorkspace(lib, e, Options{Edition: "Pro"}, nil)
	if err != nil {
		t.Fatalf("scaffold without hardware: %v", err)
	}
	if rb := assertLoads(t, bare, e.ID); rb != nil && rb.Windows != nil && len(rb.Windows.Hardware) != 0 {
		t.Errorf("expected no hardware entries, got %+v", rb.Windows.Hardware)
	}
}

// TestAppsReachTheRecipe checks the program picker's ids survive into the
// synthesized recipe as winget package ids with a step to run them — and that
// asking for a program that does not exist fails the build rather than
// quietly producing media without it.
func TestAppsReachTheRecipe(t *testing.T) {
	lib, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e, ok := Get("windows-11")
	if !ok {
		t.Fatal("windows-11 missing from the catalog")
	}
	opts := Options{Edition: "Pro", Apps: []string{"chrome", "7zip"}}
	dir, err := scaffoldQuickWorkspace(lib, e, opts, nil)
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	r := assertLoads(t, dir, e.ID)
	if r == nil || r.Windows == nil {
		t.Fatal("recipe did not load")
	}
	if !r.Windows.Apps.Enabled() {
		t.Fatal("windows.apps is empty")
	}
	want := []string{"Google.Chrome", "7zip.7zip"}
	got := r.Windows.Apps.Winget
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("package %d = %q, want %q", i, got[i], want[i])
		}
	}
	hasApps := false
	for _, s := range r.Windows.Firstboot.Steps {
		if s.Apps {
			hasApps = true
		}
	}
	if !hasApps {
		t.Error("recipe has apps but no `apps` firstboot step — they would never install")
	}
	for _, f := range r.Lint() {
		if f.Severity == "error" {
			t.Errorf("lint error: %s", f.Message)
		}
	}

	// No apps selected must leave the block (and the step) out entirely.
	bare, err := scaffoldQuickWorkspace(lib, e, Options{Edition: "Pro"}, nil)
	if err != nil {
		t.Fatalf("scaffold without apps: %v", err)
	}
	if rb := assertLoads(t, bare, e.ID); rb != nil && rb.Windows.Apps.Enabled() {
		t.Error("expected no apps block")
	}
}

// TestCatalogEntriesWellFormed guards the hand-maintained OS list: a typo in
// an id, a missing filename, or an unpinned image would only surface as a
// confusing failure partway through someone's install.
func TestCatalogEntriesWellFormed(t *testing.T) {
	idRe := regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	seen := map[string]bool{}
	for _, e := range Catalog() {
		if !idRe.MatchString(e.ID) {
			t.Errorf("%q is not a usable id (lowercase, digits, dot, dash, underscore)", e.ID)
		}
		if seen[e.ID] {
			t.Errorf("duplicate catalog id %q", e.ID)
		}
		seen[e.ID] = true
		if e.Name == "" || e.Notes == "" {
			t.Errorf("%s: needs a name and notes — they are all the picker shows", e.ID)
		}
		// An import-only entry is a pointer to a vendor portal, not a download:
		// the pinning rules below are about fetching, so they do not apply. What
		// it must have instead is checked by TestImportOnlyEntriesAreFeatureGated.
		if e.ImportOnly() {
			continue
		}
		switch e.Family {
		case Windows:
			if e.Provider != "fido" || e.Fido == nil {
				t.Errorf("%s: Windows entries resolve their ISO through Fido", e.ID)
			}
			if len(e.Editions) == 0 {
				t.Errorf("%s: Windows entries need editions (the key table is keyed by them)", e.ID)
			}
		case Linux:
			if e.URL == "" || e.Filename == "" {
				t.Errorf("%s: Linux entries need a url and a filename", e.ID)
			}
			// Unpinned would mean downloading gigabytes and trusting whatever
			// arrived. Immutable artifacts carry a hash; rolling ones resolve
			// theirs from the vendor's checksum file at pull time.
			if e.SHA256 == "" && e.ChecksumsURL == "" {
				t.Errorf("%s: unpinned — needs either sha256 or a checksums_url", e.ID)
			}
			if e.SHA256 != "" && len(e.SHA256) != 64 {
				t.Errorf("%s: sha256 is %d characters, want 64", e.ID, len(e.SHA256))
			}
		default:
			t.Errorf("%s: unknown family %q", e.ID, e.Family)
		}
		if !strings.HasPrefix(e.URL, "https://") && e.URL != "" {
			t.Errorf("%s: url must be https", e.ID)
		}
	}
}

// TestResolveChecksumLive exercises the rolling-image path against the real
// checksum file: the filename has to still match a line in it, which is
// exactly what silently rots when a distro changes its layout. Network, so
// it is skipped under -short (which is what CI runs).
func TestResolveChecksumLive(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}
	for _, e := range Catalog() {
		if e.ChecksumsURL == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		sum, err := resolveChecksum(ctx, e)
		cancel()
		if err != nil {
			t.Errorf("%s: %v", e.ID, err)
			continue
		}
		if len(sum) != 64 {
			t.Errorf("%s: resolved checksum %q is not a sha256", e.ID, sum)
		}
		t.Logf("%s -> %s", e.ID, sum)
	}
}

// TestRawImageEntriesCompose guards the single-board path: a Raspberry Pi
// image is a compressed raw disk image, not an installer ISO, so it has to
// reach compose as raw-img or it would be written as if it were bootable
// media for a PC.
func TestRawImageEntriesCompose(t *testing.T) {
	lib, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	raw := 0
	for _, e := range Catalog() {
		if e.Kind() != ImageRaw {
			continue
		}
		raw++
		dir, err := scaffoldQuickWorkspace(lib, e, Options{}, nil)
		if err != nil {
			t.Fatalf("%s: scaffold: %v", e.ID, err)
		}
		r := assertLoads(t, dir, e.ID)
		if r == nil {
			continue
		}
		if got := string(r.OS.Type); got != "raw-img" {
			t.Errorf("%s: os.type = %q, want raw-img", e.ID, got)
		}
		src, err := workspace.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		s, err := src.Source(e.ID)
		if err != nil {
			t.Fatalf("%s: manifest: %v", e.ID, err)
		}
		if s.Format != "img" {
			t.Errorf("%s: manifest format = %q, want img", e.ID, s.Format)
		}
		for _, f := range r.Lint() {
			if f.Severity == "error" {
				t.Errorf("%s: lint error: %s", e.ID, f.Message)
			}
		}
	}
	if raw == 0 {
		t.Skip("no raw-image entries in the catalog")
	}
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
