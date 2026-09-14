package cab

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// build writes a one-folder cabinet the way makecab does: the folder's stream
// cut into 32 KB blocks, each MSZIP block deflated with the previous block as
// its dictionary, so back-references cross block boundaries as in real
// catalogs.
func build(t *testing.T, comp uint16, names []string, contents [][]byte) []byte {
	t.Helper()
	var stream []byte
	var offsets []uint32
	for _, c := range contents {
		offsets = append(offsets, uint32(len(stream)))
		stream = append(stream, c...)
	}
	var blocks [][]byte
	var sizes []int
	var prev []byte
	for i := 0; i < len(stream); i += maxBlock {
		chunk := stream[i:min(i+maxBlock, len(stream))]
		sizes = append(sizes, len(chunk))
		if comp == compNone {
			blocks = append(blocks, chunk)
			continue
		}
		var buf bytes.Buffer
		buf.WriteString("CK")
		w, err := flate.NewWriterDict(&buf, flate.BestCompression, prev)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(chunk)
		w.Close()
		blocks = append(blocks, buf.Bytes())
		prev = chunk
	}

	le := binary.LittleEndian
	var files bytes.Buffer
	for i, n := range names {
		var h [16]byte
		le.PutUint32(h[0:], uint32(len(contents[i])))
		le.PutUint32(h[4:], offsets[i])
		files.Write(h[:])
		files.WriteString(n)
		files.WriteByte(0)
	}
	const headerLen, folderLen = 36, 8
	coffFiles := headerLen + folderLen
	dataStart := coffFiles + files.Len()

	var out bytes.Buffer
	h := make([]byte, headerLen)
	copy(h, "MSCF")
	le.PutUint32(h[16:], uint32(coffFiles))
	h[24], h[25] = 3, 1
	le.PutUint16(h[26:], 1)
	le.PutUint16(h[28:], uint16(len(names)))
	out.Write(h)
	f := make([]byte, folderLen)
	le.PutUint32(f[0:], uint32(dataStart))
	le.PutUint16(f[4:], uint16(len(blocks)))
	le.PutUint16(f[6:], comp)
	out.Write(f)
	out.Write(files.Bytes())
	for i, blk := range blocks {
		d := make([]byte, 8)
		le.PutUint16(d[4:], uint16(len(blk)))
		le.PutUint16(d[6:], uint16(sizes[i]))
		out.Write(d)
		out.Write(blk)
	}
	b := out.Bytes()
	le.PutUint32(b[8:], uint32(len(b)))
	return b
}

func catalogLike(n int) []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><DriverPackManifest>`)
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, `<DriverPackage id="%d"><Model name="OptiPlex %d"/></DriverPackage>`, i, 3000+i%977)
	}
	return b.Bytes()
}

func TestExtractMSZIPAcrossBlocks(t *testing.T) {
	xml := catalogLike(300_000) // ten blocks, repetitive, so references reach back
	other := []byte("hello")
	for _, comp := range []uint16{compMSZIP, compNone} {
		dir := t.TempDir()
		cabBytes := build(t, comp, []string{`sub\Catalog.xml`, "readme.txt"}, [][]byte{xml, other})
		written, err := extract(cabBytes, dir)
		if err != nil {
			t.Fatalf("comp %d: %v", comp, err)
		}
		if len(written) != 2 {
			t.Fatalf("comp %d: wrote %v", comp, written)
		}
		got, err := os.ReadFile(filepath.Join(dir, "sub", "Catalog.xml"))
		if err != nil || !bytes.Equal(got, xml) {
			t.Fatalf("comp %d: catalog differs (%d bytes, err %v)", comp, len(got), err)
		}
		if got, _ := os.ReadFile(filepath.Join(dir, "readme.txt")); !bytes.Equal(got, other) {
			t.Fatalf("comp %d: second file = %q", comp, got)
		}
	}
}

func TestExtractRefusesLZXAndEscapes(t *testing.T) {
	b := build(t, 3 /* LZX */, []string{"a"}, [][]byte{[]byte("x")})
	if _, err := extract(b, t.TempDir()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("LZX: err = %v, want ErrUnsupported", err)
	}
	dir := t.TempDir()
	b = build(t, compNone, []string{`..\..\evil.txt`}, [][]byte{[]byte("x")})
	written, err := extract(b, dir)
	if err != nil {
		t.Fatal(err)
	}
	if rel, _ := filepath.Rel(dir, written[0]); rel != "evil.txt" {
		t.Fatalf("escaping name landed at %s", written[0])
	}
	if _, err := extract([]byte("not a cab at all, just bytes here........"), dir); err == nil {
		t.Fatal("garbage accepted")
	}
}

// TestRealCatalogs expands the cached Dell and HP catalogs when a developer
// machine has them, as a check against the cabinets vendors actually publish.
func TestRealCatalogs(t *testing.T) {
	home, _ := os.UserHomeDir()
	for _, name := range []string{"DriverPackCatalog.cab", "HPClientDriverPackCatalog.cab"} {
		p := filepath.Join(home, ".local", "share", "dsky", "helpers", "catalogs", name)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		written, err := Extract(p, t.TempDir())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b, _ := os.ReadFile(written[0])
		if !bytes.HasPrefix(bytes.TrimLeft(b, "\xef\xbb\xbf \r\n"), []byte("<")) {
			t.Fatalf("%s: extracted %s does not look like XML: %q", name, written[0], b[:min(40, len(b))])
		}
		t.Logf("%s -> %s, %d bytes", name, filepath.Base(written[0]), len(b))
	}
}
