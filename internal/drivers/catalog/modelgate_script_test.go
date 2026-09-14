package catalog_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uplinkresearch/dsky/internal/drivers/catalog"
	"github.com/uplinkresearch/dsky/internal/recipe"
)

// The first-boot gate is PowerShell and the matcher that chose the bundle is
// Go. They must agree on every name, or a bundle picked for a machine would
// be skipped on it — or run on the wrong one. This runs the real script, with
// -CheckOnly, over the same cases the Go matcher is tested with.
func TestModelInstallerScriptAgreesWithGo(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("PowerShell (pwsh) is not installed here")
	}
	script := filepath.Join(t.TempDir(), recipe.ModelInstallerScriptName)
	if err := os.WriteFile(script, []byte(recipe.ModelInstallerScriptFile()), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(manufacturer, reported, listed string) string {
		t.Helper()
		out, err := exec.Command(pwsh, "-NoProfile", "-NonInteractive", "-File", script,
			"-Installer", "bundle.exe", "-Arguments", "-u", "-Vendor", "framework", "-Model", listed,
			"-ThisManufacturer", manufacturer, "-ThisModel", reported, "-CheckOnly").CombinedOutput()
		if err != nil {
			t.Fatalf("script failed: %v\n%s", err, out)
		}
		return string(out)
	}
	for _, c := range catalog.FrameworkModelCases {
		out := run("Framework", c.Reported, c.Listed)
		if ran := strings.Contains(out, "would run"); ran != c.Match {
			t.Errorf("%q on %q: script says %q, Go says match=%v", c.Listed, c.Reported, strings.TrimSpace(out), c.Match)
		}
	}
	// Another maker's computer never runs it, whatever it is called.
	if out := run("Dell Inc.", "Laptop 13 (AMD Ryzen AI 300 Series)", "Framework Laptop 13 AMD Ryzen AI 300 Series"); strings.Contains(out, "would run") {
		t.Errorf("ran on a Dell: %s", out)
	}
}
