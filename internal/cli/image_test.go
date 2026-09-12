package cli

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestLooksLikeImagePath(t *testing.T) {
	yes := []string{
		"ubuntu.iso", "UBUNTU.ISO", "disk.img", "sd.raw", "fw.bin",
		"image.img.xz", "image.img.zst", "image.iso.gz", "deck.img.bz2",
		"C:\\Users\\me\\Downloads\\thing.iso", "/home/me/thing.img",
	}
	for _, w := range yes {
		if !looksLikeImagePath(w) {
			t.Errorf("%q should be treated as an image path", w)
		}
	}
	// Recipe ids must keep routing to the builder. A word that merely contains
	// "iso" or "img" is not a path, and treating it as one would break
	// `uplink flash <recipe>` for anyone whose recipe is named that way.
	no := []string{"nuc-win11", "windows-11", "isolinux", "my-img-recipe", "iso", "img", ""}
	for _, w := range no {
		if looksLikeImagePath(w) {
			t.Errorf("%q is a recipe id, not an image path", w)
		}
	}
}

// TestArtifactForPathPrefersSidecar: a composed artifact's sidecar carries the
// recipe's minimum stick size and verify choice, which a bare file cannot
// state. Losing it would silently drop the size interlock.
func TestArtifactForPathPrefersSidecar(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "built.img")
	if err := os.WriteFile(img, []byte("image bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := `{"recipe_id":"nuc-win11","kind":"image","path":"` +
		filepath.ToSlash(img) + `","size":11,"verify":"readback-sha256","min_stick":8589934592}`
	if err := os.WriteFile(img+".json", []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	a, composed, err := artifactForPath(img)
	if err != nil {
		t.Fatal(err)
	}
	if !composed {
		t.Error("a file with a sidecar should be reported as composed, not foreign")
	}
	if a.RecipeID != "nuc-win11" || a.MinStick != 8589934592 {
		t.Errorf("sidecar was not used: %+v", a)
	}
}

// TestArtifactForPathForeignFile is the new capability: an image this tool
// never built is describable and therefore writable.
func TestArtifactForPathForeignFile(t *testing.T) {
	dir := t.TempDir()
	iso := filepath.Join(dir, "some-distro.iso")
	if err := os.WriteFile(iso, bytes.Repeat([]byte("x"), 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	a, composed, err := artifactForPath(iso)
	if err != nil {
		t.Fatal(err)
	}
	if composed {
		t.Error("a file with no sidecar must not be reported as composed")
	}
	if a.Verify != "readback-sha256" {
		t.Errorf("verification must stay on for foreign images, got %q", a.Verify)
	}
	// An uncompressed image's own size is a true minimum: a stick smaller than
	// the file cannot hold it, and saying so beats failing partway through.
	if a.MinStick != 2048 {
		t.Errorf("min stick = %d, want the file size 2048", a.MinStick)
	}
}

// TestArtifactForPathSniffsCompression: the magic bytes decide, not the name.
// A .img.xz renamed to .img is common, and trusting the name would write the
// compressed bytes to the stick and produce media that does not boot.
func TestArtifactForPathSniffsCompression(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(bytes.Repeat([]byte("payload"), 512))
	zw.Close()

	lying := filepath.Join(dir, "not-actually-raw.img")
	if err := os.WriteFile(lying, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	a, _, err := artifactForPath(lying)
	if err != nil {
		t.Fatal(err)
	}
	if a.Compress != "gz" {
		t.Errorf("compression not detected from magic bytes: got %q, want gz", a.Compress)
	}
	// A compressed image's expanded size is unknown, so no minimum can honestly
	// be claimed — claiming the compressed size would pass a stick too small
	// to hold the result.
	if a.MinStick != 0 {
		t.Errorf("min stick = %d, want 0 for a compressed image", a.MinStick)
	}
}

func TestArtifactForPathRejectsUnusable(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.iso")
	os.WriteFile(empty, nil, 0o644)
	subdir := filepath.Join(dir, "adir.iso")
	os.Mkdir(subdir, 0o755)

	for _, tc := range []struct{ name, path, want string }{
		{"missing", filepath.Join(dir, "nope.iso"), "no such file"},
		{"empty", empty, "is empty"},
		{"directory", subdir, "is a directory"},
	} {
		_, _, err := artifactForPath(tc.path)
		if err == nil {
			t.Errorf("%s: accepted an unusable image", tc.name)
			continue
		}
		if !bytes.Contains([]byte(err.Error()), []byte(tc.want)) {
			t.Errorf("%s: error %q does not say %q", tc.name, err, tc.want)
		}
	}
}
