package recipe

import (
	"strings"
	"testing"
)

func domainRecipe(d *DomainSpec) *Recipe {
	return &Recipe{
		Version: 1,
		ID:      "test-domain",
		OS:      OSSpec{Type: OSWindows, Source: "win11", SourceMode: SourceISO},
		Target:  TargetSpec{Scheme: "mbr", Filesystem: "fat32", Size: "auto", Boot: "uefi-only"},
		Flash:   FlashSpec{Verify: "readback-sha256"},
		Windows: &WindowsSpec{
			Domain:    d,
			Unattend:  &UnattendSpec{Template: "templates/autounattend.xml.tmpl"},
			Firstboot: FirstbootSpec{Mode: "generate", Log: "firstboot.log"},
		},
	}
}

// TestDomainValidation: every rejection here exists because the alternative is
// a machine that installs cleanly into a workgroup and looks fine until
// somebody tries to log in with a domain account.
func TestDomainValidation(t *testing.T) {
	cases := []struct {
		name    string
		spec    *DomainSpec
		wantErr string // substring; "" means it must validate
	}{
		{"nothing set", &DomainSpec{}, ""},
		{"credentialed complete", &DomainSpec{
			Join: "corp.example.com", Username: "svc-join",
			Password: "${var:domain_password}", OU: "OU=Workstations,DC=corp,DC=example,DC=com",
		}, ""},
		{"offline complete", &DomainSpec{Blob: "payload/pc01.odj"}, ""},
		{"offline by ref", &DomainSpec{BlobRef: "odj-pc01"}, ""},

		{"both paths", &DomainSpec{Join: "corp.example.com", Blob: "x.odj"}, "pick one"},
		{"two blobs", &DomainSpec{Blob: "a.odj", BlobRef: "b"}, "pick one"},
		{"ou with offline", &DomainSpec{Blob: "a.odj", OU: "OU=X,DC=y"}, "no effect on an offline join"},
		{"no domain", &DomainSpec{Username: "svc", Password: "p"}, "needs join"},
		{"no user", &DomainSpec{Join: "corp.example.com", Password: "p"}, "no username"},
		{"no password", &DomainSpec{Join: "corp.example.com", Username: "svc"}, "no password"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := domainRecipe(tc.spec).Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("valid spec rejected: %v", err)
			case tc.wantErr != "" && err == nil:
				t.Errorf("expected an error containing %q, got none", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestDomainModes(t *testing.T) {
	var nilSpec *DomainSpec
	if nilSpec.Enabled() || nilSpec.Offline() {
		t.Error("a nil spec must be neither enabled nor offline")
	}
	if (&DomainSpec{}).Enabled() {
		t.Error("an empty spec is not a configured join")
	}
	if !(&DomainSpec{Blob: "x.odj"}).Offline() {
		t.Error("a blob is an offline join")
	}
	if (&DomainSpec{Join: "corp.example.com"}).Offline() {
		t.Error("a credentialed join is not offline")
	}
}

// TestFirstbootVerifiesTheJoin is the countermeasure to the worst property of
// unattended domain join: Windows Setup does not report a failed join. It
// carries on, the machine finishes installing and looks entirely correct, and
// it is in a workgroup. The only moment the evidence still exists is the first
// boot, so the generated script checks then and leaves the logs where someone
// will find them.
func TestFirstbootVerifiesTheJoin(t *testing.T) {
	r := domainRecipe(&DomainSpec{
		Join: "corp.example.com", Username: "svc", Password: "${var:domain_password}",
	})
	r.Windows.Firstboot.Steps = []Step{{Drivers: true}}
	got := renderFirstboot(t, r)

	for _, want := range []string{
		"PartOfDomain",                  // the check itself
		"DOMAIN JOIN FAILED",            // says so in the log
		`C:\Windows\debug\netsetup.log`, // the log that says why
		`C:\Windows\Panther\UnattendGC\setuperr.log`,
		`C:\DOMAIN-JOIN-FAILED-netsetup.log`, // copied somewhere obvious
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated firstboot does not mention %q:\n%s", want, got)
		}
	}
	// It has to run before the other work, so the evidence is captured even if
	// a later step fails and stops the script.
	if idx, drv := strings.Index(got, "PartOfDomain"), strings.Index(got, "pnputil"); drv >= 0 && idx > drv {
		t.Error("the domain check runs after the driver step — a failing step would hide it")
	}

	// An offline join is just as silent when it fails, so it is checked too.
	off := domainRecipe(&DomainSpec{Blob: "payload/pc01.odj"})
	off.Windows.Firstboot.Steps = []Step{{Drivers: true}}
	if !strings.Contains(renderFirstboot(t, off), "PartOfDomain") {
		t.Error("an offline join is not verified at first boot")
	}

	// And a recipe with no domain join gets none of it.
	plain := domainRecipe(nil)
	plain.Windows.Firstboot.Steps = []Step{{Drivers: true}}
	if strings.Contains(renderFirstboot(t, plain), "PartOfDomain") {
		t.Error("a recipe with no domain join emitted a domain check")
	}
}

// TestDomainLintWarnsAboutCredentialsOnTheStick: the password really is
// readable by anyone holding the media, and that is worth saying every time
// rather than burying in documentation nobody reads.
func TestDomainLintWarnsAboutCredentialsOnTheStick(t *testing.T) {
	r := domainRecipe(&DomainSpec{
		Join: "corp.example.com", Username: "svc-join", Password: "hunter2",
	})
	var joined string
	for _, f := range r.Lint() {
		joined += f.Severity + ": " + f.Message + "\n"
	}
	for _, want := range []string{"clear text", "domain admin", "literal password", "no ou"} {
		if !strings.Contains(strings.ToLower(joined), want) {
			t.Errorf("lint does not mention %q:\n%s", want, joined)
		}
	}

	// An offline join puts nothing on the stick, so none of that applies and
	// warning anyway would train people to ignore the warnings.
	off := domainRecipe(&DomainSpec{Blob: "payload/pc01.odj"})
	for _, f := range off.Lint() {
		if strings.Contains(strings.ToLower(f.Message), "clear text") {
			t.Errorf("offline join warned about credentials on the stick: %s", f.Message)
		}
	}

	// A templated password is the recommended form and must not be scolded.
	good := domainRecipe(&DomainSpec{
		Join: "corp.example.com", Username: "svc-join",
		Password: "${var:domain_password}", OU: "OU=Workstations,DC=corp,DC=example,DC=com",
	})
	for _, f := range good.Lint() {
		if strings.Contains(f.Message, "literal password") || strings.Contains(f.Message, "no ou") {
			t.Errorf("the recommended form was warned about: %s", f.Message)
		}
	}
}
