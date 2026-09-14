package fsimg

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
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

func TestAcidMBR(t *testing.T) { testAcid(t, SchemeMBR, false) }
func TestAcidGPT(t *testing.T) { testAcid(t, SchemeGPT, false) }

// The one-pass writer (BuildStaged) must pass the same test, read back through
// go-diskfs, and pass fsck.fat where it is installed.
func TestAcidStagedMBR(t *testing.T) { testAcid(t, SchemeMBR, true) }
func TestAcidStagedGPT(t *testing.T) { testAcid(t, SchemeGPT, true) }

func testAcid(t *testing.T, scheme Scheme, staged bool) {
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
	opts := Options{
		Scheme:       scheme,
		Label:        "ESD-USB",
		SizeBytes:    SizeForContent(contentBytes, entries),
		Reproducible: true,
	}
	if staged {
		err = BuildStaged(img, opts, m, nil)
	} else {
		err = BuildImage(img, opts, func(fsys FS) error { return Populate(fsys, m, nil) })
	}
	if err != nil {
		t.Fatal(err)
	}
	if staged {
		fsckImage(t, img)
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

// fsckImage runs dosfstools' checker, read-only and with a verification pass,
// on partition 1 of an image.
func fsckImage(t *testing.T, img string) {
	t.Helper()
	fsck, err := exec.LookPath("fsck.fat")
	if err != nil {
		t.Log("fsck.fat not installed; skipping the dosfstools check")
		return
	}
	src, err := os.Open(img)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	part := filepath.Join(t.TempDir(), "part.img")
	dst, err := os.Create(part)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, io.NewSectionReader(src, partStartSector*SectorSize, 1<<62)); err != nil {
		t.Fatal(err)
	}
	dst.Close()
	out, err := exec.Command(fsck, "-n", "-V", part).CombinedOutput()
	if err != nil {
		t.Fatalf("fsck.fat found problems: %v\n%s", err, out)
	}
}

func TestStagedReproducibleAndDotDot(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1756800000")
	dir := t.TempDir()
	src := filepath.Join(dir, "tree")
	for _, rel := range []string{"alpha/a.txt", "beta/b.txt", "beta/nested/c.txt", "gamma/d.txt", "setup.exe", "Mixed Case Name.TXT", "empty.txt"} {
		p := filepath.Join(src, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		data := []byte(rel)
		if rel == "empty.txt" {
			data = nil
		}
		os.WriteFile(p, data, 0o644)
	}
	m := StageMap{}
	if err := m.AddTree(src, "/"); err != nil {
		t.Fatal(err)
	}
	for _, scheme := range []Scheme{SchemeMBR, SchemeGPT} {
		opts := Options{Scheme: scheme, Label: "ESD-USB", SizeBytes: 256 << 20, Reproducible: true}
		var hashes [2]string
		for i := range hashes {
			img := filepath.Join(dir, fmt.Sprintf("r%d.img", i))
			if err := BuildStaged(img, opts, m, nil); err != nil {
				t.Fatal(err)
			}
			f, _ := os.Open(img)
			hashes[i] = hashFile(t, f)
			f.Close()
		}
		if hashes[0] != hashes[1] {
			t.Errorf("%s: identical builds differ", scheme)
		}
	}
	opts := Options{Scheme: SchemeMBR, Label: "ESD-USB", SizeBytes: 256 << 20, Reproducible: true}
	if err := BuildStaged(filepath.Join(dir, "r0.img"), opts, m, nil); err != nil {
		t.Fatal(err)
	}
	// Windows shows no partitions on a read-only attached MBR disk whose
	// signature is zero.
	raw, _ := os.ReadFile(filepath.Join(dir, "r0.img"))
	if sig := binary.LittleEndian.Uint32(raw[440:444]); sig == 0 {
		t.Error("MBR disk signature is zero")
	}
	img := filepath.Join(dir, "r0.img")
	got, err := rootDotDotClusters(img, partStartSector*SectorSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("found %d top-level directories, want 3", len(got))
	}
	for c, dotdot := range got {
		if dotdot != 0 {
			t.Errorf("directory at cluster %d has .. cluster %d, want 0", c, dotdot)
		}
	}
	sizes, err := ReadTreeSizes(img)
	if err != nil || len(sizes) != len(m) || sizes["/empty.txt"] != 0 || sizes["/Mixed Case Name.TXT"] != int64(len("Mixed Case Name.TXT")) {
		t.Fatalf("read back %v, %v", sizes, err)
	}
	fsckImage(t, img)
}
