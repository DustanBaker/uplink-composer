package oscatalog

import (
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uplinkresearch/dsky/internal/library"
	"github.com/uplinkresearch/dsky/internal/recipe"
)

// TestQuickTemplateIsValidXMLInEveryMode renders the Install dialog's answer
// file for each combination that changes its shape, and checks every
// namespace prefix is declared. Setup rejects an answer file with an undeclared
// one, and Go's own decoder does not complain: the requirement-check bypass
// used wcm:action with no wcm declaration in scope, so every stick built with
// "Skip TPM / Secure Boot / RAM checks" carried an answer file Setup refuses.
func TestQuickTemplateIsValidXMLInEveryMode(t *testing.T) {
	tmpl, err := templatesFS.ReadFile("templates/autounattend.xml.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "autounattend.xml.tmpl")
	if err := os.WriteFile(path, tmpl, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, bypass := range []string{"0", "1"} {
		for _, mode := range []string{"", "offline", "credentialed", "by_serial"} {
			for _, account := range []string{"local", "oobe"} {
				vars := map[string]string{
					"locale": "en-US", "computer_name": "*", "edition_key": "KEY",
					"admin_user": "user", "admin_display_name": "User", "admin_password": "",
					"account_mode": account, "bypass_requirements": bypass, "domain_mode": mode,
					"domain_odj_blob": "QUJD", "domain_join": "corp.example.com", "domain_ou": "",
					"domain_user": "u", "domain_account_domain": "CORP", "domain_password": "p",
				}
				out, err := recipe.RenderTemplate(path, recipe.Context{Vars: vars})
				if err != nil {
					t.Fatalf("bypass=%s mode=%q account=%s: %v", bypass, mode, account, err)
				}
				if p := undeclaredPrefix(t, out); p != "" {
					t.Errorf("bypass=%s mode=%q account=%s: undeclared namespace prefix on %s", bypass, mode, account, p)
				}
				if mode == "by_serial" && (!strings.Contains(out, recipe.DomainSerialScriptName) || strings.Contains(out, "<ComputerName>")) {
					t.Errorf("by_serial: join script missing or a computer name set")
				}
			}
		}
	}
}

func undeclaredPrefix(t *testing.T, doc string) string {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(doc))
	bound := func(space string) bool { return space == "" || strings.Contains(space, ":") || space == "xmlns" }
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return ""
		}
		if err != nil {
			t.Fatalf("not XML: %v\n%s", err, doc)
		}
		if se, ok := tok.(xml.StartElement); ok {
			if !bound(se.Name.Space) {
				return se.Name.Space + ":" + se.Name.Local
			}
			for _, a := range se.Attr {
				if !bound(a.Name.Space) {
					return se.Name.Local + " @" + a.Name.Space + ":" + a.Name.Local
				}
			}
		}
	}
}

// TestBatchDomainJoinReachesTheRecipe: the Install dialog's folder of join
// files becomes windows.domain.blobs_by_serial in the recipe it builds from.
func TestBatchDomainJoinReachesTheRecipe(t *testing.T) {
	lib, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e, _ := Get("windows-11")
	dir := filepath.Join(t.TempDir(), "Join files")
	dir = filepath.ToSlash(dir)
	ws, err := scaffoldQuickWorkspace(lib, e, Options{Edition: "Pro", DomainBlobsDir: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := assertLoads(t, ws, e.ID)
	if r.Windows.Domain == nil || r.Windows.Domain.BlobsBySerial != dir || !r.Windows.Domain.BySerial() {
		t.Fatalf("domain = %+v", r.Windows.Domain)
	}
}
