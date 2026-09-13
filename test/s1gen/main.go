// Command s1gen builds the S1 acid-test artifacts for native-OS verification:
// a FAT32 image populated with a synthetic nasty tree, a fixed-VHD wrapper of
// it (so Windows Mount-DiskImage can attach it), and a JSON manifest of every
// file's SHA-256. test/s1-verify.ps1 (elevated) mounts the VHD, runs chkdsk,
// and compares hashes using the platform's own FAT driver.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"

	"github.com/uplinkresearch/dsky/internal/fsimg"
	"github.com/uplinkresearch/dsky/internal/vhd"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: s1gen <output-dir>")
		os.Exit(2)
	}
	outDir := os.Args[1]
	if err := run(outDir); err != nil {
		fmt.Fprintln(os.Stderr, "s1gen:", err)
		os.Exit(1)
	}
}

func run(outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	treeDir := filepath.Join(outDir, "tree")
	if err := os.RemoveAll(treeDir); err != nil {
		return err
	}
	manifest, err := makeTree(treeDir)
	if err != nil {
		return err
	}

	m := fsimg.StageMap{}
	if err := m.AddTree(treeDir, "/"); err != nil {
		return err
	}
	bytes, entries, err := m.Stats()
	if err != nil {
		return err
	}
	img := filepath.Join(outDir, "s1.img")
	err = fsimg.BuildImage(img, fsimg.Options{
		Scheme:       fsimg.SchemeMBR,
		Label:        "ESD-USB",
		SizeBytes:    fsimg.SizeForContent(bytes, entries),
		Reproducible: true,
	}, func(fsys fsimg.FS) error { return fsimg.Populate(fsys, m, nil) })
	if err != nil {
		return err
	}

	vhdPath := filepath.Join(outDir, "s1.vhd")
	if err := vhd.Wrap(img, vhdPath); err != nil {
		return err
	}

	mf, err := os.Create(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		return err
	}
	enc := json.NewEncoder(mf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifest); err != nil {
		mf.Close()
		return err
	}
	if err := mf.Close(); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d files, %d MiB content)\n", vhdPath, len(manifest), bytes>>20)
	return nil
}

// makeTree mirrors the fsimg acid-test tree: LFNs, $OEM$ names, deep paths,
// hundreds of entries, multi-MiB binaries. Returns relative path -> sha256.
func makeTree(root string) (map[string]string, error) {
	rng := rand.New(rand.NewSource(42))
	manifest := map[string]string{}
	write := func(rel string, size int) error {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		data := make([]byte, size)
		rng.Read(data)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		manifest[rel] = hex.EncodeToString(sum[:])
		return nil
	}
	files := []struct {
		rel  string
		size int
	}{
		{"autounattend.xml", 4966},
		{"efi/boot/bootx64.efi", 1 << 20},
		{"sources/ei.cfg", 55},
		{"sources/boot.wim", 64 << 20},
		{"sources/install.swm", 512 << 20},
		{"sources/install2.swm", 256 << 20},
		{"sources/$OEM$/$$/Setup/Scripts/install-agent.cmd", 1671},
		{"sources/$OEM$/$$/Setup/Scripts/ScreenConnect.ClientSetup.msi", 8 << 20},
		{"sources/$OEM$/$$/Setup/Scripts/Drivers/Intel LAN i225-i226 (W11)/e2fn.inf", 102405},
		{"sources/$OEM$/$$/Setup/Scripts/Drivers/Intel LAN i225-i226 (W11)/e2fn.sys", 1471576},
		{"$WinpeDriver$/iaStorVD/iaStorVD.inf", 8192},
		{"some.dir.v2/file.name.with.many.dots.and.a.rather.long.tail.txt", 128},
	}
	for _, f := range files {
		if err := write(f.rel, f.size); err != nil {
			return nil, err
		}
	}
	deep := "deep"
	for i := 0; i < 12; i++ {
		deep += fmt.Sprintf("/nested-directory-%02d", i)
	}
	if err := write(deep+"/leaf.txt", 64); err != nil {
		return nil, err
	}
	for i := 0; i < 300; i++ {
		if err := write(fmt.Sprintf("many/Entry Number %03d With A Long Name.dat", i), 100+i); err != nil {
			return nil, err
		}
	}
	for i := 0; i < 100; i++ {
		if err := write(fmt.Sprintf("many83/F%03d.TXT", i), 10+i); err != nil {
			return nil, err
		}
	}
	return manifest, nil
}
