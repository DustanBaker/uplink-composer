package compose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uplinkresearch/dsky/internal/driverresolve"
	"github.com/uplinkresearch/dsky/internal/drivers/catalog"
	"github.com/uplinkresearch/dsky/internal/fsimg"
	"github.com/uplinkresearch/dsky/internal/library"
	"github.com/uplinkresearch/dsky/internal/manifest"
	"github.com/uplinkresearch/dsky/internal/recipe"
	"github.com/uplinkresearch/dsky/internal/workspace"
)

// A Framework driver bundle, found by model, goes onto the stick with the
// model gate, and first boot runs it through the gate with -u — never
// directly, because on the wrong mainboard Framework's script stops at a
// prompt. The pack is written into the workspace by the same AddPack the
// driver search uses, so the manifest under test is the one a real search
// writes.
func TestComposeFrameworkBundleIsGated(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1756800000")
	root := t.TempDir()
	wsDir := filepath.Join(root, "ws")
	if err := workspace.Scaffold(wsDir, "Test Org"); err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(root, "master")
	writeFile(t, filepath.Join(tree, "efi", "boot", "bootx64.efi"), make([]byte, 1<<20))
	writeFile(t, filepath.Join(tree, "sources", "boot.wim"), make([]byte, 1<<20))
	writeFile(t, filepath.Join(tree, "sources", "install.swm"), make([]byte, 1<<20))

	lib, err := library.Open(filepath.Join(root, "lib"))
	if err != nil {
		t.Fatal(err)
	}
	bundle := []byte("7zS SFX stub + install_drivers.bat, in spirit")
	sum := sha256.Sum256(bundle)
	const model = "Framework Laptop 13 AMD Ryzen AI 300 Series"
	pack := catalog.Pack{
		Vendor: catalog.Framework, Model: model, OS: "win11", Version: "v2.01", Released: "2026-08-17",
		URL:    "https://downloads.frame.work/driver/Framework13_AMD_RyzenAI300_drivers_W11_v201_2026_08_06.exe",
		SHA256: hex.EncodeToString(sum[:]), Format: "exe", Install: "exe", Args: catalog.FrameworkArgs,
	}
	ws, err := workspace.Load(wsDir)
	if err != nil {
		t.Fatal(err)
	}
	feed, err := catalog.FeedFor("framework", catalog.NewCache(lib.HelpersDir()))
	if err != nil {
		t.Fatal(err)
	}
	ref := manifest.HardwareRef{Vendor: "framework", Model: model, OS: "win11"}
	id, err := driverresolve.AddPack(context.Background(), ws, lib, feed, pack, ref, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	man, err := os.ReadFile(filepath.Join(wsDir, "manifests", id+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"install: exe", `args: ["-u"]`, "vendor: framework", model} {
		if !strings.Contains(string(man), want) {
			t.Errorf("manifest missing %q:\n%s", want, man)
		}
	}
	src, err := manifest.Load(filepath.Join(wsDir, "manifests", id+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	host := filepath.Join(root, "bundle.exe")
	writeFile(t, host, bundle)
	if _, err := lib.Import(src, host); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(wsDir, "recipes", "fw.yaml"), []byte(`version: 1
id: fw
name: "Framework test"
os:
  type: windows
  source_mode: tree
  tree_path: `+filepath.ToSlash(tree)+`
target:
  volume_label: ESD-USB
  min_stick: 1GiB
windows:
  ei_cfg: { edition: Professional, channel: Retail, vl: false }
  unattend:
    template: templates/autounattend.xml.tmpl
    vars:
      edition_key: VK7JG-NPHTM-C97JM-9MPGT-3V66T
      locale: en-US
  hardware:
    - { vendor: framework, model: "`+model+`", os: win11 }
  firstboot:
    mode: generate
    steps:
      - drivers
`))
	if ws, err = workspace.Load(wsDir); err != nil {
		t.Fatal(err)
	}
	r, err := ws.Recipe("fw")
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
	for _, want := range []string{scripts + "/" + src.Filename, scripts + "/" + recipe.ModelInstallerScriptName} {
		if _, ok := sizes[want]; !ok {
			t.Errorf("missing from image: %s", want)
		}
	}
	// Reads from the image come back padded to a whole cluster, so the size
	// comes from the directory entry and the bytes are compared up to it.
	// Generated .ps1 files ship with a UTF-8 BOM: Windows PowerShell 5.1
	// reads BOM-less scripts in the ANSI code page.
	want := "\xef\xbb\xbf" + recipe.ModelInstallerScriptFile()
	gatePath := scripts + "/" + recipe.ModelInstallerScriptName
	if sizes[gatePath] != int64(len(want)) {
		t.Errorf("gate script on the stick is %d bytes, want %d", sizes[gatePath], len(want))
	}
	if gate := readImageFile(t, art.Path, gatePath); !strings.HasPrefix(gate, want) {
		t.Error("the gate script on the stick is not the one DSKY generates")
	}
	firstboot := readImageFile(t, art.Path, scripts+"/firstboot.cmd")
	wantLine := `powershell -NoProfile -ExecutionPolicy Bypass -File "%SCRIPTS%\` + recipe.ModelInstallerScriptName +
		`" -Installer "%SCRIPTS%\` + src.Filename + `" -Arguments "-u" -Vendor "framework" -Model "` + model + `"`
	if !strings.Contains(firstboot, wantLine) {
		t.Errorf("firstboot does not run the bundle through the gate:\n%s", firstboot)
	}
	if strings.Contains(firstboot, `  "%SCRIPTS%\`+src.Filename+`"`) {
		t.Errorf("firstboot also runs the bundle directly:\n%s", firstboot)
	}
}
