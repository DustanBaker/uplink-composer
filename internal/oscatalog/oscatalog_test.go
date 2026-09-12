package oscatalog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DustanBaker/uplink-composer/internal/buildinfo"
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

// TestCatalogURLsLive is the link-rot alarm. Distros move and prune: Garuda
// and PikaOS publish no permanent alias and delete old builds outright, and a
// pinned URL that has become a 404 is invisible here — the tests pass, the
// index publishes, and the first person to learn is a user whose download
// fails.
//
// It checks reachability, not content. Verifying the bytes would mean
// downloading well over a hundred gigabytes; the pinned hash already catches
// a changed file at pull time, and this catches the case that check never
// reaches. Every entry is reported before failing, so one run says everything
// that rotted rather than only the first thing.
//
// Network, so it is skipped under -short. CI runs -short; the scheduled
// catalog-health workflow is what runs this.
func TestCatalogURLsLive(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}
	client := &http.Client{Timeout: 90 * time.Second}
	for _, e := range Catalog() {
		if e.ImportOnly() {
			continue // nothing to fetch by design
		}
		if e.URL == "" {
			continue // Windows resolves through Fido at pull time
		}
		size, err := reachable(client, e.URL)
		switch {
		case err != nil:
			t.Errorf("%s: %s\n    %v", e.ID, e.URL, err)
		// Every entry here is an OS image; anything this small is an error
		// page or a stub that happened to return 200.
		case size >= 0 && size < 100<<20:
			t.Errorf("%s: %s\n    returned only %d bytes — not an OS image", e.ID, e.URL, size)
		default:
			t.Logf("%s: ok (%d MiB)", e.ID, size>>20)
		}
	}
}

// reachable reports the size the server declares for a URL. HEAD first, since
// it costs nothing; some hosts refuse it, so a refusal falls back to asking
// for the first byte rather than being reported as rot.
func reachable(client *http.Client, url string) (int64, error) {
	req, err := http.NewRequest(http.MethodHead, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	resp, err := client.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return resp.ContentLength, nil
		}
	}

	rangeReq, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	rangeReq.Header.Set("User-Agent", buildinfo.UserAgent())
	rangeReq.Header.Set("Range", "bytes=0-0")
	r2, err2 := client.Do(rangeReq)
	if err2 != nil {
		return 0, err2
	}
	defer r2.Body.Close()
	_, _ = io.Copy(io.Discard, r2.Body)
	if r2.StatusCode != http.StatusOK && r2.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("HEAD and ranged GET both refused (GET returned HTTP %d)", r2.StatusCode)
	}
	// A 206 states the full length after the slash in Content-Range.
	if cr := r2.Header.Get("Content-Range"); cr != "" {
		if i := strings.LastIndex(cr, "/"); i >= 0 {
			if n, err := strconv.ParseInt(cr[i+1:], 10, 64); err == nil {
				return n, nil
			}
		}
	}
	return -1, nil // reachable, size not stated
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
