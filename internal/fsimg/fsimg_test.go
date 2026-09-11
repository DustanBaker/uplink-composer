package fsimg

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// makeAcidTree writes a synthetic tree exercising FAT32 weak spots: long
// file names, $OEM$-style names, deep nesting, unicode, many entries per
// directory, and multi-MiB binaries. Returns imgPath→hostPath expectations.
func makeAcidTree(t *testing.T, root string) StageMap {
	t.Helper()
	rng := rand.New(rand.NewSource(42))
	write := func(rel string, size int) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		data := make([]byte, size)
		rng.Read(data)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("autounattend.xml", 4966)
	write("efi/boot/bootx64.efi", 1<<20)
	write("sources/ei.cfg", 55)
	write("sources/boot.wim", 6<<20)
	write("sources/install.swm", 24<<20)
	write("sources/install2.swm", 12<<20)
	write("sources/$OEM$/$$/Setup/Scripts/install-agent.cmd", 1671)
	write("sources/$OEM$/$$/Setup/Scripts/ScreenConnect.ClientSetup.msi", 2<<20)
	write("sources/$OEM$/$$/Setup/Scripts/Drivers/Intel LAN i225-i226 (W11)/e2fn.inf", 102405)
	write("sources/$OEM$/$$/Setup/Scripts/Drivers/Intel LAN i225-i226 (W11)/e2fn.sys", 1471576)
	write("$WinpeDriver$/iaStorVD/iaStorVD.inf", 8192)
	write("some.dir.v2/file.name.with.many.dots.and.a.rather.long.tail.txt", 128)
	deep := "deep"
	for i := 0; i < 12; i++ {
		deep += fmt.Sprintf("/nested-directory-%02d", i)
	}
	write(deep+"/leaf.txt", 64)
	// One directory with many entries, mixed 8.3-safe and LFN names.
	for i := 0; i < 300; i++ {
		write(fmt.Sprintf("many/Entry Number %03d With A Long Name.dat", i), 100+i)
	}
	for i := 0; i < 100; i++ {
		write(fmt.Sprintf("many83/F%03d.TXT", i), 10+i)
	}

	m := StageMap{}
	if err := m.AddTree(root, "/"); err != nil {
		t.Fatal(err)
	}
	return m
}

func hashFile(t *testing.T, r io.Reader) string {
	t.Helper()
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestAcidMBR(t *testing.T) { testAcid(t, SchemeMBR) }
func TestAcidGPT(t *testing.T) { testAcid(t, SchemeGPT) }

func testAcid(t *testing.T, scheme Scheme) {
	t.Setenv("SOURCE_DATE_EPOCH", "1756800000") // 2025-09-02, the stick's birthday

	dir := t.TempDir()
	src := filepath.Join(dir, "tree")
	m := makeAcidTree(t, src)
	contentBytes, entries, err := m.Stats()
	if err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(dir, "acid.img")
	start := time.Now()
	err = BuildImage(img, Options{
		Scheme:       scheme,
		Label:        "ESD-USB",
		SizeBytes:    SizeForContent(contentBytes, entries),
		Reproducible: true,
	}, func(fsys FS) error {
		return Populate(fsys, m, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("built %d files / %d MiB in %s", len(m), contentBytes>>20, time.Since(start).Round(time.Millisecond))

	// Every staged file must read back byte-identical.
	sizes, err := ReadTreeSizes(img)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(sizes), len(m); got != want {
		t.Errorf("image holds %d files, want %d", got, want)
	}
	for imgPath, hostPath := range m {
		st, err := os.Stat(hostPath)
		if err != nil {
			t.Fatal(err)
		}
		if sizes[imgPath] != st.Size() {
			t.Errorf("%s: size %d in image, want %d", imgPath, sizes[imgPath], st.Size())
			continue
		}
		in, closeImg, err := OpenImageFile(img, imgPath)
		if err != nil {
			t.Fatalf("%s: open in image: %v", imgPath, err)
		}
		gotHash := hashFile(t, in)
		in.Close()
		closeImg()
		srcF, err := os.Open(hostPath)
		if err != nil {
			t.Fatal(err)
		}
		wantHash := hashFile(t, srcF)
		srcF.Close()
		if gotHash != wantHash {
			t.Errorf("%s: content hash mismatch", imgPath)
		}
	}
}

// TestReproducible builds the same tree twice and requires identical images.
func TestReproducible(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1756800000")
	dir := t.TempDir()
	src := filepath.Join(dir, "tree")
	m := makeAcidTree(t, src)
	contentBytes, entries, err := m.Stats()
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Scheme: SchemeMBR, Label: "ESD-USB", SizeBytes: SizeForContent(contentBytes, entries), Reproducible: true}
	var hashes [2]string
	for i := range hashes {
		img := filepath.Join(dir, fmt.Sprintf("r%d.img", i))
		if err := BuildImage(img, opts, func(fsys FS) error { return Populate(fsys, m, nil) }); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(img)
		if err != nil {
			t.Fatal(err)
		}
		hashes[i] = hashFile(t, f)
		f.Close()
	}
	if hashes[0] != hashes[1] {
		t.Errorf("images differ across identical builds: %s vs %s", hashes[0], hashes[1])
	}
}

// TestRootDotDotZero asserts the FAT-spec repair fixRootDotDot applies: every
// top-level directory's ".." entry must store cluster 0 (chkdsk flags the
// image otherwise).
func TestRootDotDotZero(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tree")
	for _, rel := range []string{"alpha/a.txt", "beta/b.txt", "beta/nested/c.txt", "gamma/d.txt"} {
		p := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(rel), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := StageMap{}
	if err := m.AddTree(src, "/"); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(dir, "dots.img")
	err := BuildImage(img, Options{Scheme: SchemeMBR, Label: "T", SizeBytes: 256 << 20}, func(fsys FS) error {
		return Populate(fsys, m, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := rootDotDotClusters(img, partStartSector*SectorSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("found %d top-level directories, want 3: %v", len(got), got)
	}
	for c, dotdot := range got {
		if dotdot != 0 {
			t.Errorf("directory at cluster %d has .. cluster %d, want 0", c, dotdot)
		}
	}
}

func TestRejectsOversizeFile(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a sparse >4GiB file")
	}
	dir := t.TempDir()
	big := filepath.Join(dir, "install.wim")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxFileSize + 1); err != nil {
		f.Close()
		t.Skip("filesystem cannot create sparse test file")
	}
	f.Close()
	m := StageMap{}
	m.AddFile(big, "/sources/install.wim")
	img := filepath.Join(dir, "big.img")
	err = BuildImage(img, Options{Scheme: SchemeMBR, Label: "X", SizeBytes: 256 << 20}, func(fsys FS) error {
		return Populate(fsys, m, nil)
	})
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("4 GiB")) {
		t.Errorf("want 4 GiB limit error, got %v", err)
	}
}
