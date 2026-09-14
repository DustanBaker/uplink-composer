package compose

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/uplinkresearch/dsky/internal/fsimg"
	"github.com/uplinkresearch/dsky/internal/library"
	"github.com/uplinkresearch/dsky/internal/recipe"
	"github.com/uplinkresearch/dsky/internal/workspace"
)

// savefile writes provisioning data as `djoin /savefile` does.
func savefile(t *testing.T, path, b64 string) {
	t.Helper()
	u := utf16.Encode([]rune(b64 + "\x00"))
	out := []byte{0xFF, 0xFE}
	for _, c := range u {
		out = append(out, byte(c), byte(c>>8))
	}
	writeFile(t, path, out)
}

func TestSerialBlobsRefusesWhatWouldNotJoin(t *testing.T) {
	good := func(t *testing.T) string {
		dir := t.TempDir()
		savefile(t, filepath.Join(dir, "5cg 1234abc.txt"), "QUJD")
		writeFile(t, filepath.Join(dir, "5CG1234ABD.txt"), []byte("REVG\r\n")) // /printblob form
		return dir
	}
	blobs, err := SerialBlobs(good(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(blobs) != 2 || blobs[0].Serial != "5CG1234ABC" || blobs[0].Base64 != "QUJD" || blobs[1].Base64 != "REVG" {
		t.Fatalf("blobs = %+v", blobs)
	}

	cases := map[string]func(dir string){
		"duplicate serial": func(d string) { writeFile(t, filepath.Join(d, "5CG1234ABC .txt"), []byte("QUJD")) },
		"placeholder":      func(d string) { writeFile(t, filepath.Join(d, "To be filled by O.E.M..txt"), []byte("QUJD")) },
		"not a join file":  func(d string) { writeFile(t, filepath.Join(d, "PC9.txt"), []byte("not base64 at all!")) },
		"other file type":  func(d string) { writeFile(t, filepath.Join(d, "5CG9.blob"), []byte("QUJD")) },
		"bad name":         func(d string) { writeFile(t, filepath.Join(d, "PC#42.txt"), []byte("QUJD")) },
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			dir := good(t)
			spoil(dir)
			if _, err := SerialBlobs(dir); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	if _, err := SerialBlobs(t.TempDir()); err == nil {
		t.Fatal("empty folder accepted")
	}
	// Hidden files a desktop leaves behind are not join files and not errors.
	dir := good(t)
	writeFile(t, filepath.Join(dir, ".DS_Store"), []byte{0})
	if _, err := SerialBlobs(dir); err != nil {
		t.Fatalf("hidden file: %v", err)
	}
}

// unboundPrefix finds an element or attribute whose namespace prefix was never
// declared. Go's decoder leaves such a prefix in Name.Space instead of failing,
// and Windows Setup rejects the answer file.
func unboundPrefix(t *testing.T, doc string) string {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(doc))
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return ""
		}
		if err != nil {
			t.Fatalf("not XML: %v", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		bound := func(space string) bool {
			return space == "" || strings.Contains(space, ":") || space == "xmlns"
		}
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

func TestRenderJoinBySerial(t *testing.T) {
	doc := renderScaffolded(t, &recipe.DomainSpec{BlobsBySerial: "join-files"})
	sp := specializeSection(t, doc)
	for _, want := range []string{"Microsoft-Windows-Deployment", recipe.DomainSerialScriptName} {
		if !strings.Contains(sp, want) {
			t.Errorf("specialize pass missing %q", want)
		}
	}
	if strings.Contains(sp, "<ComputerName>") || strings.Contains(sp, "<AccountData>") || strings.Contains(sp, "<JoinDomain>") {
		t.Error("a name or another join is set beside the by-serial join; the join file names the computer")
	}
	if p := unboundPrefix(t, doc); p != "" {
		t.Fatalf("undeclared namespace prefix on %s", p)
	}
	if err := checkDomainRendered(doc, &recipe.DomainSpec{BlobsBySerial: "x"}, "t"); err != nil {
		t.Fatal(err)
	}
	if err := checkDomainRendered(renderScaffolded(t, nil), &recipe.DomainSpec{BlobsBySerial: "x"}, "t"); err == nil {
		t.Fatal("a template without the block was not caught")
	}
}

// TestComposeJoinBySerial builds a whole stick and reads back what is on it.
func TestComposeJoinBySerial(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1756800000")
	root := t.TempDir()
	wsDir := filepath.Join(root, "ws")
	if err := workspace.Scaffold(wsDir, "Test Org"); err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(root, "master")
	writeFile(t, filepath.Join(tree, "efi", "boot", "bootx64.efi"), make([]byte, 1<<16))
	writeFile(t, filepath.Join(tree, "sources", "install.wim"), make([]byte, 1<<20))
	joins := filepath.Join(root, "join files")
	savefile(t, filepath.Join(joins, "5cg1234abc.txt"), "QUJD")
	writeFile(t, filepath.Join(joins, "5CG1234ABD.txt"), []byte("REVG"))

	writeFile(t, filepath.Join(wsDir, "recipes", "batch.yaml"), []byte(`version: 1
id: batch
name: batch
os:
  type: windows
  source_mode: tree
  tree_path: `+filepath.ToSlash(tree)+`
target:
  volume_label: ESD-USB
  min_stick: 1GiB
windows:
  unattend:
    template: templates/autounattend.xml.tmpl
    vars: { edition_key: KEY, locale: en-US }
  domain:
    blobs_by_serial: "`+filepath.ToSlash(joins)+`"
`))
	lib, err := library.Open(filepath.Join(root, "lib"))
	if err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Load(wsDir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := ws.Recipe("batch")
	if err != nil {
		t.Fatal(err)
	}
	art, err := Build(context.Background(), Request{Workspace: ws, Library: lib, Recipe: r})
	if err != nil {
		t.Fatal(err)
	}
	sizes, err := fsimg.ReadTreeSizes(art.Path)
	if err != nil {
		t.Fatal(err)
	}
	scripts := "/sources/$OEM$/$$/Setup/Scripts"
	for _, want := range []string{
		scripts + "/" + recipe.DomainSerialScriptName,
		scripts + "/dsky-domain/5CG1234ABC.txt",
		scripts + "/dsky-domain/5CG1234ABD.txt",
	} {
		if _, ok := sizes[want]; !ok {
			t.Errorf("missing from image: %s", want)
		}
	}
	// Both arrive in the form djoin /loadfile reads, whichever was supplied.
	for serial, want := range map[string]string{"5CG1234ABC": "QUJD", "5CG1234ABD": "REVG"} {
		got, err := decodeODJ([]byte(readImageFile(t, art.Path, scripts+"/dsky-domain/"+serial+".txt")))
		if err != nil || got != want {
			t.Errorf("%s on the stick decodes to %q, %v", serial, got, err)
		}
		raw := readImageFile(t, art.Path, scripts+"/dsky-domain/"+serial+".txt")
		if !strings.HasPrefix(raw, "\xFF\xFE") {
			t.Errorf("%s is not UTF-16 with a byte-order mark, as djoin /savefile writes", serial)
		}
	}
	unattend := readImageFile(t, art.Path, "/autounattend.xml")
	if !strings.Contains(unattend, recipe.DomainSerialScriptName) {
		t.Error("the answer file does not run the join script")
	}
	if p := unboundPrefix(t, unattend); p != "" {
		t.Errorf("undeclared namespace prefix on %s", p)
	}
	fb := readImageFile(t, art.Path, scripts+"/firstboot.cmd")
	if !strings.Contains(fb, "DOMAIN-JOIN-FAILED-serial-number.log") {
		t.Error("a failed join does not leave the serial-number log where a technician will find it")
	}
	if !strings.Contains(fmt.Sprint(r.Lint()), "every computer's join file") {
		t.Error("lint does not warn that the stick carries every computer's credential")
	}
	_ = os.Remove
}
