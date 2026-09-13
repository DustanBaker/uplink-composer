package stream

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// payload is deliberately compressible so every codec produces a real
// container rather than a stored block.
var payload = bytes.Repeat([]byte("bootwright composer raw image payload\n"), 512)

// The standard library can decompress bzip2 but not produce it, so this
// fixture is a real `bzip2 -9` of payload, committed rather than generated:
// the test must not need a bzip2 binary on the machine running it.
const bz2Base64 = "QlpoOTFBWSZTWfun4bgACf/RgAAQQAA+5tygMAEYAUyYmQZGFMmJkGRgVVGmjIDZJuAIAEgC4AgAY" +
	"AGABQAQALgDkAdAD0ASAKACABsALADcAZAFQB0AMgCQBIA8AFQBYAegD4AUAGABUAZAGoA+AEgDUA" +
	"fi7kinChIfdPw3AA=="

var bz2Payload = mustBase64(bz2Base64)

func mustBase64(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func write(t *testing.T, dir, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestDetectAndOpen covers every compression the flash engine claims to
// sniff. bzip2 is the one that matters most here: the Steam Deck recovery
// image ships as .img.bz2, and before it was handled the file would have
// been written to a stick still compressed — which boots nothing.
func TestDetectAndOpen(t *testing.T) {
	dir := t.TempDir()

	var gz bytes.Buffer
	gw := gzip.NewWriter(&gz)
	gw.Write(payload)
	gw.Close()

	var xzb bytes.Buffer
	xw, err := xz.NewWriter(&xzb)
	if err != nil {
		t.Fatal(err)
	}
	xw.Write(payload)
	xw.Close()

	var zst bytes.Buffer
	zw, err := zstd.NewWriter(&zst)
	if err != nil {
		t.Fatal(err)
	}
	zw.Write(payload)
	zw.Close()

	cases := []struct {
		name, want string
		body       []byte
	}{
		{"plain.img", "none", payload},
		{"image.img.gz", "gz", gz.Bytes()},
		{"image.img.xz", "xz", xzb.Bytes()},
		{"image.img.zst", "zstd", zst.Bytes()},
		{"image.img.bz2", "bz2", bz2Payload},
	}
	for _, c := range cases {
		path := write(t, dir, c.name, c.body)
		got, err := Detect(path)
		if err != nil {
			t.Errorf("%s: Detect: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: Detect = %q, want %q", c.name, got, c.want)
			continue
		}
		r, err := Open(path, got)
		if err != nil {
			t.Errorf("%s: Open: %v", c.name, err)
			continue
		}
		out, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Errorf("%s: reading: %v", c.name, err)
			continue
		}
		if !bytes.Equal(out, payload) {
			t.Errorf("%s: decompressed %d bytes, want the original %d", c.name, len(out), len(payload))
		}
	}
}

// TestDetectShortFile: a truncated or empty file must report an error rather
// than be mistaken for uncompressed data and written to a device.
func TestDetectShortFile(t *testing.T) {
	p := write(t, t.TempDir(), "tiny.img", []byte{'B'})
	if _, err := Detect(p); err == nil {
		t.Error("expected an error for a file too short to sniff")
	}
}

func TestOpenUnknownCompression(t *testing.T) {
	p := write(t, t.TempDir(), "x.img", payload)
	if _, err := Open(p, "lzma"); err == nil {
		t.Error("expected an error for an unknown compression")
	}
}
