package catalog

import (
	"os"
	"testing"
)

// A trimmed copy of Framework's real downloads page for the Laptop 13 AMD
// Ryzen AI 300 Series (2026-09-14). The bundle it names was downloaded and
// hashed while writing this: the SHA-256 below is what it hashed to, and the
// same value the page publishes.
func TestParseFrameworkPage(t *testing.T) {
	page, err := os.ReadFile("testdata/framework-laptop-13-amd-ryzen-ai-300-series.html")
	if err != nil {
		t.Fatal(err)
	}
	p, ok := parseFrameworkPage(page, "win11")
	if !ok {
		t.Fatal("no Windows 11 bundle found on the page")
	}
	want := Pack{
		Vendor: Framework, Model: "Framework Laptop 13 AMD Ryzen AI 300 Series", OS: "win11",
		Version: "v2.01", Released: "2026-08-17", Format: "exe", Install: "exe",
		URL:    "https://downloads.frame.work/driver/Framework13_AMD_RyzenAI300_drivers_W11_v201_2026_08_06.exe",
		SHA256: "3ab7dc9e8d7dd8673c3f91e815e94856fd13d000c24c3cc4c6dc5b6952a4609a",
	}
	if p.Vendor != want.Vendor || p.Model != want.Model || p.OS != want.OS || p.Version != want.Version ||
		p.Released != want.Released || p.URL != want.URL || p.SHA256 != want.SHA256 || p.Format != want.Format || p.Install != want.Install {
		t.Errorf("parsed\n %+v\nwant\n %+v", p, want)
	}
	if len(p.Args) != 1 || p.Args[0] != "-u" {
		t.Errorf("args %q, want the unattended switch", p.Args)
	}
	if _, ok := parseFrameworkPage(page, "win10"); ok {
		t.Error("a Windows 11 bundle was offered for Windows 10")
	}
}

// FrameworkModelCases pair the model a Framework reports to Windows with the
// listed models it must, and must not, match. The reported names include the
// irregular ones (the 11th gen reports just "Laptop"; the 7040 series has no
// space before "Series").
var FrameworkModelCases = []struct {
	Reported, Listed string
	Match            bool
}{
	{"Laptop 13 (AMD Ryzen AI 300 Series)", "Framework Laptop 13 AMD Ryzen AI 300 Series", true},
	{"Laptop 13 (AMD Ryzen AI 300 Series)", "Framework Laptop 16 AMD Ryzen AI 300 Series", false},
	{"Laptop 13 (AMD Ryzen AI 300 Series)", "Framework Laptop 13 AMD Ryzen 7040 Series", false},
	{"Laptop 13 (AMD Ryzen 7040Series)", "Framework Laptop 13 AMD Ryzen 7040 Series", true},
	{"Laptop 16 (AMD Ryzen 7040 Series)", "Framework Laptop 16 AMD Ryzen 7040 Series", true},
	{"Laptop 16 (AMD Ryzen 7040 Series)", "Framework Laptop 13 AMD Ryzen 7040 Series", false},
	{"Laptop (12th Gen Intel Core)", "Framework Laptop 13 12th Gen Intel Core", true},
	{"Laptop (12th Gen Intel Core)", "Framework Laptop 13 13th Gen Intel Core", false},
	{"Laptop 12 (13th Gen Intel Core)", "Framework Laptop 12 13th Gen Intel Core", true},
	{"Laptop 12 (13th Gen Intel Core)", "Framework Laptop 13 13th Gen Intel Core", false},
	{"Laptop", "Framework Laptop 13 11th Gen Intel Core", false},
	{"Desktop (AMD Ryzen AI Max 300 Series)", "Framework Desktop AMD Ryzen AI Max 300 Series", true},
	{"Framework Laptop 13 AMD Ryzen AI 300 Series", "Framework Laptop 13 AMD Ryzen AI 300 Series", true},
}

func TestFrameworkModelMatches(t *testing.T) {
	for _, c := range FrameworkModelCases {
		if got := FrameworkModelMatches(c.Reported, c.Listed); got != c.Match {
			t.Errorf("%q vs %q: %v, want %v", c.Reported, c.Listed, got, c.Match)
		}
	}
}
