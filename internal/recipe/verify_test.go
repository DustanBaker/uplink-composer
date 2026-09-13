package recipe

import (
	"strings"
	"testing"
)

func verifyRecipe(mutate func(*WindowsSpec)) *Recipe {
	w := &WindowsSpec{
		EICfg:    &EICfg{Edition: "Professional"},
		Unattend: &UnattendSpec{Template: "t.tmpl", Vars: map[string]string{"admin_user": "user", "account_mode": "local"}},
		Firstboot: FirstbootSpec{Mode: "generate", Log: "firstboot.log",
			Steps: []Step{{Drivers: true}}},
	}
	if mutate != nil {
		mutate(w)
	}
	return &Recipe{
		Version: 1, ID: "test-verify",
		OS:      OSSpec{Type: OSWindows, Source: "win11", SourceMode: SourceISO},
		Target:  TargetSpec{Scheme: "mbr", Filesystem: "fat32", Size: "auto", Boot: "uefi-only"},
		Flash:   FlashSpec{Verify: "readback-sha256"},
		Windows: w,
	}
}

func renderVerify(r *Recipe, d ResolvedDrivers) string {
	return GenerateVerifyPS(r, d, func(ref string) (string, error) { return ref + ".msi", nil })
}

// TestVerifyChecksTheSilentFailures: the point of this script is the things
// nobody notices. A machine that installed cleanly and is in a workgroup, or
// has a yellow-banged NIC, looks exactly like a success.
func TestVerifyChecksTheSilentFailures(t *testing.T) {
	r := verifyRecipe(func(w *WindowsSpec) {
		w.Domain = &DomainSpec{Join: "corp.example.com", Username: "svc", Password: "${var:pw}"}
	})
	got := renderVerify(r, ResolvedDrivers{})

	for _, want := range []string{
		"ConfigManagerErrorCode -ne 0", // yellow bangs
		"PartOfDomain",                 // the workgroup case
		"Test-ComputerSecureChannel",   // joined but trust broken
		"corp.example.com",             // the right domain, not just any
		"DOMAIN-JOIN-FAILED",           // points at the evidence
		"firstboot.log",                // did the script even run
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the check never looks for %q", want)
		}
	}
	// It must be read-only: this runs on a machine somebody is about to hand
	// over, and a "verify" step that changes something is a trap.
	for _, forbidden := range []string{
		"Remove-Item", "Set-ItemProperty", "New-Item", "Stop-Process",
		"Restart-Computer", "Add-Computer", "Start-Process",
	} {
		if strings.Contains(got, forbidden) {
			t.Errorf("the check calls %s — it must only report", forbidden)
		}
	}
}

// TestVerifyIsSilentAboutWhatWasNotAsked: a check that reports on a domain
// nobody asked to join, or programs nobody chose, is noise — and noise is how
// people learn to ignore the output.
func TestVerifyIsSilentAboutWhatWasNotAsked(t *testing.T) {
	got := renderVerify(verifyRecipe(nil), ResolvedDrivers{})
	for _, unwanted := range []string{"PartOfDomain", "Test-ComputerSecureChannel", "Uninstall\\*"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("a recipe with no domain and no apps still checks for %q", unwanted)
		}
	}
	// But the universal checks are always there.
	for _, want := range []string{"ConfigManagerErrorCode", "firstboot.log", "Get-LocalUser"} {
		if !strings.Contains(got, want) {
			t.Errorf("the always-applicable check %q is missing", want)
		}
	}
}

func TestVerifyChecksRequestedPrograms(t *testing.T) {
	r := verifyRecipe(func(w *WindowsSpec) {
		w.Apps = &AppsSpec{Winget: []string{"Google.Chrome", "7zip.7zip"}}
		w.Firstboot.Steps = []Step{{Drivers: true}, {Apps: true},
			{MSI: &RunItem{Ref: "app-macula"}}}
	})
	got := renderVerify(r, ResolvedDrivers{})
	for _, want := range []string{
		"Google.Chrome", "Chrome", // the id reported, the display name matched
		"7zip.7zip", "7zip", //
		"app-macula.msi",               // the operator's own installer, by staged name
		"CurrentVersion\\Uninstall\\*", // how it looks
	} {
		if !strings.Contains(got, want) {
			t.Errorf("programs check is missing %q", want)
		}
	}
}

// TestVerifyWithOwnInstallerAndNoWingetApps is the ordinary MSP recipe: an
// agent MSI at first boot and no public packages at all. The programs section
// is reached for the installer, so anything that assumes winget apps exist
// panics on a nil spec — which is how this was found.
func TestVerifyWithOwnInstallerAndNoWingetApps(t *testing.T) {
	r := verifyRecipe(func(w *WindowsSpec) {
		w.Apps = nil
		w.Firstboot.Steps = []Step{{Drivers: true}, {MSI: &RunItem{Ref: "app-macula"}}}
	})
	got := renderVerify(r, ResolvedDrivers{}) // must not panic
	if !strings.Contains(got, "app-macula.msi") {
		t.Error("the operator's own installer is not checked")
	}
	if strings.Contains(got, "is NOT installed") {
		t.Error("checked for winget packages when none were requested")
	}
}

// TestVerifyExitCodeIsUsable: this gets run across a bench of machines, so the
// answer has to be machine-readable, not just coloured text.
func TestVerifyExitCodeIsUsable(t *testing.T) {
	got := renderVerify(verifyRecipe(nil), ResolvedDrivers{})
	for _, code := range []string{"exit 0", "exit 1", "exit 2"} {
		if !strings.Contains(got, code) {
			t.Errorf("the check never returns %q, so it cannot be scripted over twenty machines", code)
		}
	}
	if !strings.Contains(got, "DOES NOT MATCH THE BUILD") {
		t.Error("a failing machine is not called out unmistakably")
	}
}

// TestVerifyTellsRunningApartFromFailed is the difference between a check
// people trust and one they learn to ignore. Installing programs at first boot
// takes minutes and needs the network, so someone running this at the first
// logon prompt would otherwise get a red report that only means "not yet".
func TestVerifyTellsRunningApartFromFailed(t *testing.T) {
	got := renderVerify(verifyRecipe(nil), ResolvedDrivers{})
	for _, want := range []string{
		"LastWriteTime",               // how recently first boot wrote
		"STILL RUNNING",               // said plainly
		"exit 2",                      // and distinguishable by a script
		"-Wait",                       // how to watch it finish
		"has not been written to for", // the genuinely-stuck wording
	} {
		if !strings.Contains(got, want) {
			t.Errorf("cannot tell a running first boot from a failed one: missing %q", want)
		}
	}
}

// TestVerifyUsesCRLF: it is a .ps1 written to FAT32 and run by Windows;
// everything else this tool generates for Windows is CRLF too.
func TestVerifyUsesCRLF(t *testing.T) {
	got := renderVerify(verifyRecipe(nil), ResolvedDrivers{})
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Error("the script has bare LF line endings")
	}
}

// TestVerifyEditionMatchesCaption: Win32_OperatingSystem says "Pro", the
// ei.cfg EditionID says "Professional". Comparing them directly would fail on
// a correctly built machine, which is the worst kind of false alarm.
func TestVerifyEditionMatchesCaption(t *testing.T) {
	for _, tc := range []struct{ edition, want string }{
		{"Professional", "Pro"},
		{"ProfessionalN", "Pro N"},
		{"Core", "Home"},
		{"Enterprise", "Enterprise"},
	} {
		if got := editionMatch(tc.edition); got != tc.want {
			t.Errorf("editionMatch(%q) = %q, want %q", tc.edition, got, tc.want)
		}
	}
}

// TestWingetDisplayHintIsSafeInARegex: the hint goes straight into a
// PowerShell -match, so an unescaped metacharacter would either throw or match
// the wrong thing.
func TestWingetDisplayHintIsSafeInARegex(t *testing.T) {
	cases := map[string]string{
		"Google.Chrome":                     "Chrome",
		"Microsoft.VisualStudioCode":        "VisualStudioCode",
		"Notepad++.Notepad++":               `Notepad\+\+`,
		"Adobe.Acrobat.Reader.64-bit":       "Acrobat Reader 64-bit",
		"TheDocumentFoundation.LibreOffice": "LibreOffice",
	}
	for pkg, want := range cases {
		if got := wingetDisplayHint(pkg); got != want {
			t.Errorf("wingetDisplayHint(%q) = %q, want %q", pkg, got, want)
		}
	}
}
