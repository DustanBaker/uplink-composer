package compose

import (
	"encoding/base64"
	"encoding/xml"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DustanBaker/uplink-composer/internal/recipe"
	"github.com/DustanBaker/uplink-composer/internal/workspace"
)

// renderScaffolded renders the unattend template a real `uplink init` produces,
// with the vars a real build would supply. Testing the shipped template rather
// than a fixture is the point: a domain block that is correct in a test file
// and absent from what people actually get would be worse than no test.
func renderScaffolded(t *testing.T, d *recipe.DomainSpec) string {
	t.Helper()
	wsDir := filepath.Join(t.TempDir(), "ws")
	if err := workspace.Scaffold(wsDir, "Test Org"); err != nil {
		t.Fatal(err)
	}
	return renderIn(t, wsDir, d)
}

// renderIn renders an already-scaffolded workspace, for tests that need to put
// a file into it first.
func renderIn(t *testing.T, wsDir string, d *recipe.DomainSpec) string {
	t.Helper()
	uvars := map[string]string{
		"locale": "en-US", "computer_name": "*", "edition_key": "KEY",
		"admin_user": "user", "admin_display_name": "User", "admin_password": "",
		"account_mode": "local", "bypass_requirements": "0",
	}
	if err := domainVars(uvars, wsDir, d, map[string]string{"pw": "s3cret"}, nil); err != nil {
		t.Fatalf("domainVars: %v", err)
	}
	out, err := recipe.RenderTemplate(
		filepath.Join(wsDir, "templates", "autounattend.xml.tmpl"),
		recipe.Context{Vars: uvars})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// A malformed answer file is rejected by Setup with nothing helpful said,
	// so the document must at least parse.
	if err := xml.Unmarshal([]byte(out), new(struct {
		XMLName xml.Name
	})); err != nil {
		t.Fatalf("rendered unattend is not well-formed XML: %v\n%s", err, out)
	}
	return out
}

// specializeSection returns just the specialize pass. The join must be in that
// pass and no other: the same element in offlineServicing is a different,
// differently-named thing, and putting it in the wrong pass is a silent no-op.
func specializeSection(t *testing.T, doc string) string {
	t.Helper()
	start := strings.Index(doc, `<settings pass="specialize">`)
	if start < 0 {
		t.Fatal("no specialize pass in the rendered unattend")
	}
	end := strings.Index(doc[start:], "</settings>")
	if end < 0 {
		t.Fatal("specialize pass is not closed")
	}
	return doc[start : start+end]
}

func TestRenderCredentialedJoin(t *testing.T) {
	doc := renderScaffolded(t, &recipe.DomainSpec{
		Join:     "corp.example.com",
		Username: `CORP\svc-join`,
		Password: "${var:pw}",
		OU:       "OU=Workstations,DC=corp,DC=example,DC=com",
	})
	sp := specializeSection(t, doc)

	for _, want := range []string{
		`name="Microsoft-Windows-UnattendedJoin"`,
		`<Domain>CORP</Domain>`,         // the account's domain
		`<Username>svc-join</Username>`, // stripped of the DOMAIN\ prefix
		`<Password>s3cret</Password>`,   // resolved from vars, not a placeholder
		`<JoinDomain>corp.example.com</JoinDomain>`,
		`<MachineObjectOU>OU=Workstations,DC=corp,DC=example,DC=com</MachineObjectOU>`,
	} {
		if !strings.Contains(sp, want) {
			t.Errorf("specialize pass is missing %s\n%s", want, sp)
		}
	}
	// Provisioning wins over Credentials when both are present, so a
	// credentialed join must not emit one.
	for _, unwanted := range []string{"<Provisioning>", "<AccountData>", "<MachinePassword>"} {
		if strings.Contains(doc, unwanted) {
			t.Errorf("a credentialed join emitted %s, which belongs to another join path", unwanted)
		}
	}
	if err := checkDomainRendered(doc, &recipe.DomainSpec{Join: "x", Username: "u", Password: "p"}, "t"); err != nil {
		t.Errorf("the build-time check rejected a correctly rendered join: %v", err)
	}
}

func TestRenderOfflineJoin(t *testing.T) {
	blob := base64.StdEncoding.EncodeToString([]byte("machine account data"))
	wsDir := filepath.Join(t.TempDir(), "ws")
	if err := workspace.Scaffold(wsDir, "Test Org"); err != nil {
		t.Fatal(err)
	}
	// The documented form: workspace-relative. Written the way djoin's
	// /savefile does it, as UTF-16 with a byte-order mark.
	if err := writeBytes(filepath.Join(wsDir, "payload", "pc01.odj"), utf16le(blob, true)); err != nil {
		t.Fatal(err)
	}

	doc := renderIn(t, wsDir, &recipe.DomainSpec{Blob: "payload/pc01.odj"})
	sp := specializeSection(t, doc)

	if !strings.Contains(sp, "<Provisioning>") || !strings.Contains(sp, "<AccountData>"+blob+"</AccountData>") {
		t.Errorf("offline join did not render its blob into the specialize pass\n%s", sp)
	}
	// The BOM must not survive into the XML: Setup reports only "AccountData
	// data could not be base64 decoded". Written as bytes rather than an
	// escape so this source file contains no byte-order mark of its own.
	bom := string([]byte{0xEF, 0xBB, 0xBF}) // U+FEFF in UTF-8
	if strings.Contains(doc, bom) {
		t.Error("a byte-order mark reached the answer file — Setup would refuse the blob")
	}
	for _, unwanted := range []string{"<Credentials>", "<JoinDomain>", "<Password>"} {
		if strings.Contains(sp, unwanted) {
			t.Errorf("an offline join emitted %s — no user credential should appear", unwanted)
		}
	}
}

// TestOfflineBlobAbsolutePath: a blob is produced on a domain-joined machine
// and copied over, so it frequently lives outside the workspace. Joining an
// absolute path to the workspace root produces a path that cannot exist.
func TestOfflineBlobAbsolutePath(t *testing.T) {
	blob := base64.StdEncoding.EncodeToString([]byte("machine account data"))
	outside := filepath.Join(t.TempDir(), "pc02.odj")
	if err := writeBytes(outside, utf16le(blob, true)); err != nil {
		t.Fatal(err)
	}
	doc := renderScaffolded(t, &recipe.DomainSpec{Blob: outside})
	if !strings.Contains(doc, "<AccountData>"+blob+"</AccountData>") {
		t.Error("a blob given by absolute path was not picked up")
	}
}

// TestRenderWithoutDomainIsUnchanged: the overwhelming majority of recipes ask
// for no domain at all, and they must render exactly as before — with
// missingkey=error, an undefined domain_mode would break every one of them.
func TestRenderWithoutDomainIsUnchanged(t *testing.T) {
	doc := renderScaffolded(t, nil)
	for _, unwanted := range []string{"UnattendedJoin", "<JoinDomain>", "<AccountData>"} {
		if strings.Contains(doc, unwanted) {
			t.Errorf("a recipe with no domain join emitted %s", unwanted)
		}
	}
	if !strings.Contains(doc, "<ComputerName>") {
		t.Error("the specialize pass lost its usual contents")
	}
}
