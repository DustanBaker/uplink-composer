package compose

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"github.com/uplinkresearch/dsky/internal/recipe"
)

// writeBytes is a tiny helper so the render tests can lay down a blob file.
func writeBytes(path string, b []byte) error { return os.WriteFile(path, b, 0o644) }

// utf16le encodes text the way djoin's /savefile does, optionally with the
// byte-order mark it actually writes.
func utf16le(s string, bom bool) []byte {
	var out []byte
	if bom {
		out = append(out, 0xFF, 0xFE)
	}
	for _, r := range s {
		out = append(out, byte(r), byte(r>>8))
	}
	return out
}

// TestDecodeODJAcceptsWhatDjoinProduces: /savefile writes UTF-16 with a BOM
// and a trailing NUL, /print writes plain text, and people use both. Reading
// one as the other puts NULs or mojibake into the XML, which Setup rejects
// with nothing useful to say — on a machine that is no longer in front of you.
func TestDecodeODJAcceptsWhatDjoinProduces(t *testing.T) {
	want := base64.StdEncoding.EncodeToString([]byte("provisioning data for one machine"))

	cases := []struct {
		name string
		raw  []byte
	}{
		{"plain text", []byte(want)},
		{"plain text with newline", []byte(want + "\r\n")},
		{"utf-16 with BOM", utf16le(want, true)},
		{"utf-16 without BOM", utf16le(want, false)},
		{"utf-16 with trailing NUL", utf16le(want+"\x00", true)},
		// djoin wraps long output; the base64 must be rejoined, not truncated.
		{"wrapped across lines", []byte(want[:8] + "\r\n" + want[8:])},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeODJ(tc.raw)
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if got != want {
				t.Errorf("decoded to %q, want %q", got, want)
			}
		})
	}
}

func TestDecodeODJRejectsRubbish(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		raw        []byte
	}{
		{"empty", "empty", nil},
		{"whitespace only", "empty", []byte("   \r\n")},
		{"not base64", "not base64", []byte("this is clearly not a provisioning blob!!")},
		{"big-endian utf-16", "big-endian", []byte{0xFE, 0xFF, 0x00, 0x41}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeODJ(tc.raw)
			if err == nil {
				t.Fatal("accepted something that is not a provisioning blob")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// TestCheckDomainRenderedCatchesAStaleTemplate is the guard that matters most.
// A workspace scaffolded before this feature renders an unattend with no
// domain block, and without this check the build would succeed, the install
// would succeed, and the machine would be in a workgroup — found out days
// later when a domain login fails.
func TestCheckDomainRenderedCatchesAStaleTemplate(t *testing.T) {
	stale := `<unattend><settings pass="specialize"><ComputerName>*</ComputerName></settings></unattend>`

	cred := &recipe.DomainSpec{Join: "corp.example.com", Username: "svc", Password: "p"}
	err := checkDomainRendered(stale, cred, "templates/autounattend.xml.tmpl")
	if err == nil {
		t.Fatal("a template with no join block was accepted")
	}
	for _, want := range []string{"templates/autounattend.xml.tmpl", "workgroup", markerCredentialed} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}

	offline := &recipe.DomainSpec{Blob: "payload/pc01.odj"}
	if err := checkDomainRendered(stale, offline, "t.tmpl"); err == nil {
		t.Error("an offline join against a stale template was accepted")
	} else if !strings.Contains(err.Error(), markerOffline) {
		t.Errorf("offline error should name %s: %v", markerOffline, err)
	}

	// And the real thing passes.
	if err := checkDomainRendered(`<JoinDomain>corp.example.com</JoinDomain>`, cred, "t"); err != nil {
		t.Errorf("a rendered credentialed join was rejected: %v", err)
	}
	if err := checkDomainRendered(`<AccountData>abc</AccountData>`, offline, "t"); err != nil {
		t.Errorf("a rendered offline join was rejected: %v", err)
	}
	// No domain configured: nothing to check.
	if err := checkDomainRendered(stale, nil, "t"); err != nil {
		t.Errorf("a recipe with no domain join was checked anyway: %v", err)
	}
}

// TestDomainVarsSplitsTheAccountDomain: Windows wants the account's own domain
// separately from the domain being joined, and a join account is routinely
// written DOMAIN\user or user@domain.
func TestDomainVarsSplitsTheAccountDomain(t *testing.T) {
	cases := []struct{ user, wantAccount, wantDomain string }{
		{"svc-join", "svc-join", "corp.example.com"},
		{`CORP\svc-join`, "svc-join", "CORP"},
		{"svc-join@corp.example.com", "svc-join", "corp.example.com"},
	}
	for _, tc := range cases {
		uvars := map[string]string{}
		d := &recipe.DomainSpec{Join: "corp.example.com", Username: tc.user, Password: "${var:pw}"}
		err := domainVars(uvars, t.TempDir(), d, map[string]string{"pw": "secret"}, nil)
		if err != nil {
			t.Fatalf("%s: %v", tc.user, err)
		}
		if uvars["domain_user"] != tc.wantAccount || uvars["domain_account_domain"] != tc.wantDomain {
			t.Errorf("%s -> user=%q domain=%q, want %q/%q", tc.user,
				uvars["domain_user"], uvars["domain_account_domain"], tc.wantAccount, tc.wantDomain)
		}
		// The password must be resolved, never passed through as a placeholder.
		if uvars["domain_password"] != "secret" {
			t.Errorf("password not expanded: %q", uvars["domain_password"])
		}
	}
}

// TestDomainVarsFailsOnAMissingSecret: writing media with a literal
// ${var:domain_password} in it would produce a stick that silently never
// joins.
func TestDomainVarsFailsOnAMissingSecret(t *testing.T) {
	d := &recipe.DomainSpec{Join: "corp.example.com", Username: "svc", Password: "${var:nope}"}
	err := domainVars(map[string]string{}, t.TempDir(), d, map[string]string{}, nil)
	if err == nil {
		t.Fatal("a missing password var was accepted")
	}
	if !strings.Contains(err.Error(), "password") {
		t.Errorf("error does not say which field: %v", err)
	}
}
