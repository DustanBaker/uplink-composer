package recipe

import (
	"strings"
	"testing"
)

func appsRecipe(scope string, pkgs ...string) *Recipe {
	return &Recipe{
		Version: 1,
		ID:      "test-apps",
		OS:      OSSpec{Type: OSWindows, Source: "win11", SourceMode: SourceISO},
		Target:  TargetSpec{Scheme: "mbr", Filesystem: "fat32", Size: "auto", Boot: "uefi-only"},
		Flash:   FlashSpec{Verify: "readback-sha256"},
		Windows: &WindowsSpec{
			Apps:      &AppsSpec{Winget: pkgs, Scope: scope},
			Firstboot: FirstbootSpec{Mode: "generate", Log: "firstboot.log"},
		},
	}
}

func renderFirstboot(t *testing.T, r *Recipe) string {
	t.Helper()
	fb, err := GenerateFirstboot(r, ResolvedDrivers{}, func(ref string) (string, error) {
		return ref, nil
	})
	if err != nil {
		t.Fatalf("GenerateFirstboot: %v", err)
	}
	return fb
}

// TestGenerateAppsPS covers the three things that make first-boot winget
// fragile: winget itself may not exist yet, the network may not be up, and
// machine scope is refused by user-only packages.
func TestGenerateAppsPS(t *testing.T) {
	ps := GenerateAppsPS(appsRecipe("", "Google.Chrome", "7zip.7zip"))

	for _, want := range []string{
		"'Google.Chrome',",
		"'7zip.7zip',",
		"function Find-Winget", // waits for App Installer
		"function Wait-Online", // waits for the network
		"'--scope', 'machine'", // machine scope attempted
		"-1978335189",          // already-installed treated as success
		"$failed",              // failures collected, not fatal
	} {
		if !strings.Contains(ps, want) {
			t.Errorf("apps.ps1 missing %q", want)
		}
	}
	if !strings.Contains(ps, "\r\n") {
		t.Error("apps.ps1 is not CRLF")
	}
	// Machine scope must be an extra attempt layered on the plain command, so
	// a user-only package still installs rather than being skipped.
	if n := strings.Count(ps, "$tries +="); n < 2 {
		t.Errorf("expected a scope fallback attempt, got %d $tries appends", n)
	}

	// scope: user must not ask for machine scope at all.
	user := GenerateAppsPS(appsRecipe("user", "Google.Chrome"))
	if strings.Contains(user, "'--scope', 'machine'") {
		t.Error("scope: user still requested machine scope")
	}

	// Single quotes in a package id would otherwise break out of the string.
	odd := GenerateAppsPS(appsRecipe("", "We'ird.Pkg"))
	if !strings.Contains(odd, "'We''ird.Pkg'") {
		t.Error("package id with a quote was not escaped for PowerShell")
	}
}

// TestAppsFirstbootStep checks the generated firstboot invokes apps.ps1, and
// does it after drivers and debloat — programs need the network the driver
// brings up, and must not race app removal.
func TestAppsFirstbootStep(t *testing.T) {
	r := appsRecipe("", "Google.Chrome")
	r.Windows.Debloat = &DebloatSpec{Preset: "standard"}
	r.Windows.Firstboot.Steps = []Step{{Drivers: true}}

	fb := renderFirstboot(t, r)
	iApps := strings.Index(fb, `apps.ps1`)
	iDebloat := strings.Index(fb, `debloat.ps1`)
	iPnp := strings.Index(fb, `pnputil`)
	if iApps < 0 {
		t.Fatalf("firstboot never runs apps.ps1:\n%s", fb)
	}
	if iDebloat < 0 || iDebloat > iApps {
		t.Errorf("apps must run after debloat (debloat at %d, apps at %d)", iDebloat, iApps)
	}
	if iPnp >= 0 && iPnp > iApps {
		t.Errorf("apps must run after the driver sweep (pnputil at %d, apps at %d)", iPnp, iApps)
	}

	// An explicit step must not be duplicated by the auto-insert.
	r.Windows.Firstboot.Steps = []Step{{Drivers: true}, {Apps: true}}
	fb2 := renderFirstboot(t, r)
	if n := strings.Count(fb2, "apps.ps1"); n != 1 {
		t.Errorf("apps.ps1 invoked %d times, want 1", n)
	}
}

// TestAppsValidation guards the recipe-level rules.
func TestAppsValidation(t *testing.T) {
	// An `apps` step with nothing to install is an authoring mistake.
	r := appsRecipe("")
	r.Windows.Firstboot.Steps = []Step{{Apps: true}}
	if err := r.Validate(); err == nil {
		t.Error("expected an error for an apps step with no packages")
	}
	bad := appsRecipe("everywhere", "Google.Chrome")
	if err := bad.Validate(); err == nil {
		t.Error("expected an error for an unknown apps.scope")
	}
	ok := appsRecipe("machine", "Google.Chrome")
	ok.Windows.Firstboot.Steps = []Step{{Apps: true}}
	if err := ok.Validate(); err != nil {
		t.Errorf("valid apps recipe rejected: %v", err)
	}
}
