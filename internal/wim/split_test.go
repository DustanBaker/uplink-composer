package wim

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// buildWIM writes a small WIM by hand: uncompressed resources, metadata
// resources flagged as such, a blob table and XML. Enough structure for the
// splitter; the real formats are refereed in CI by wimlib and DISM.
func buildWIM(t *testing.T, images int, blobs [][]byte, version uint32, resFlags byte) (string, map[[20]byte][]byte) {
	t.Helper()
	var body bytes.Buffer
	body.Write(make([]byte, headerSize))
	content := map[[20]byte][]byte{}
	var table []byte
	add := func(data []byte, flags byte) reshdr {
		r := reshdr{size: int64(len(data)), flags: flags, offset: int64(body.Len()), origSize: int64(len(data))}
		body.Write(data)
		e := make([]byte, blobEntrySize)
		r.put(e)
		binary.LittleEndian.PutUint16(e[24:], 1)
		binary.LittleEndian.PutUint32(e[26:], 1)
		h := sha1.Sum(data)
		copy(e[30:], h[:])
		table = append(table, e...)
		content[h] = data
		return r
	}
	var boot reshdr
	for i := 0; i < images; i++ {
		r := add(bytes.Repeat([]byte{byte('M' + i)}, 1000+i), resMetadata|resFlags)
		if i == 0 {
			boot = r
		}
	}
	for _, b := range blobs {
		add(b, resFlags)
	}
	tableRes := reshdr{size: int64(len(table)), flags: resMetadata, offset: int64(body.Len()), origSize: int64(len(table))}
	body.Write(table)
	xml := []byte("\xff\xfe<\x00W\x00I\x00M\x00>\x00")
	xmlRes := reshdr{size: int64(len(xml)), offset: int64(body.Len()), origSize: int64(len(xml))}
	body.Write(xml)

	b := body.Bytes()
	copy(b, magic)
	binary.LittleEndian.PutUint32(b[8:], headerSize)
	binary.LittleEndian.PutUint32(b[12:], version)
	binary.LittleEndian.PutUint32(b[16:], 0x00040002)
	binary.LittleEndian.PutUint32(b[20:], 32768)
	copy(b[24:40], "0123456789abcdef")
	binary.LittleEndian.PutUint16(b[40:], 1)
	binary.LittleEndian.PutUint16(b[42:], 1)
	binary.LittleEndian.PutUint32(b[44:], uint32(images))
	tableRes.put(b[48:])
	xmlRes.put(b[72:])
	boot.put(b[96:])
	binary.LittleEndian.PutUint32(b[120:], 1)
	p := filepath.Join(t.TempDir(), "install.wim")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p, content
}

func TestSplit(t *testing.T) {
	var blobs [][]byte
	for i := 0; i < 40; i++ {
		blobs = append(blobs, bytes.Repeat([]byte{byte(i)}, 20000+i*1000))
	}
	blobs = append(blobs, bytes.Repeat([]byte{0xAB}, 300000)) // bigger than a part
	src, content := buildWIM(t, 2, blobs, versionDefault, 0)

	out := filepath.Join(t.TempDir(), "install.swm")
	parts, err := Split(src, out, 200000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) < 4 || filepath.Base(parts[1]) != "install2.swm" {
		t.Fatalf("parts = %v", parts)
	}
	seen := map[[20]byte]bool{}
	for i, p := range parts {
		b, _ := os.ReadFile(p)
		pn, tp := binary.LittleEndian.Uint16(b[40:]), binary.LittleEndian.Uint16(b[42:])
		flags := binary.LittleEndian.Uint32(b[16:])
		if int(pn) != i+1 || int(tp) != len(parts) || flags&flagSpanned == 0 || string(b[24:40]) != "0123456789abcdef" {
			t.Errorf("%s: part %d/%d flags %#x guid %q", filepath.Base(p), pn, tp, flags, b[24:40])
		}
		if binary.LittleEndian.Uint32(b[44:]) != 2 {
			t.Errorf("%s: image count changed", filepath.Base(p))
		}
		if x := readReshdr(b[72:]); !bytes.Equal(b[x.offset:x.offset+x.size], []byte("\xff\xfe<\x00W\x00I\x00M\x00>\x00")) {
			t.Errorf("%s: XML not carried", filepath.Base(p))
		}
		boot := readReshdr(b[96:])
		if (i == 0) != (boot.size > 0) {
			t.Errorf("%s: boot metadata header %+v", filepath.Base(p), boot)
		}
		tr := readReshdr(b[48:])
		table := b[tr.offset : tr.offset+tr.size]
		var meta int
		for j := 0; j < len(table); j += blobEntrySize {
			e := table[j : j+blobEntrySize]
			r := readReshdr(e)
			var h [20]byte
			copy(h[:], e[30:])
			if binary.LittleEndian.Uint16(e[24:]) != pn {
				t.Errorf("entry in part %d tagged %d", pn, binary.LittleEndian.Uint16(e[24:]))
			}
			if !bytes.Equal(b[r.offset:r.offset+r.size], content[h]) {
				t.Errorf("part %d: resource bytes differ", pn)
			}
			if r.flags&resMetadata != 0 {
				meta++
			}
			if seen[h] {
				t.Errorf("resource in two parts")
			}
			seen[h] = true
		}
		if (i == 0 && meta != 2) || (i > 0 && meta != 0) {
			t.Errorf("part %d has %d metadata resources", pn, meta)
		}
		if i == 0 && boot.size > 0 && !bytes.Equal(b[boot.offset:boot.offset+boot.size], bytes.Repeat([]byte{'M'}, 1000)) {
			t.Error("boot metadata header points at the wrong resource")
		}
	}
	if len(seen) != len(content) {
		t.Fatalf("parts hold %d resources, source %d", len(seen), len(content))
	}
}

func TestSplitRefuses(t *testing.T) {
	solid, _ := buildWIM(t, 1, [][]byte{{1, 2, 3}}, 0xe00, 0)
	solidRes, _ := buildWIM(t, 1, [][]byte{{1, 2, 3}}, versionDefault, resSolid)
	junk := filepath.Join(t.TempDir(), "x.wim")
	os.WriteFile(junk, bytes.Repeat([]byte("x"), 400), 0o644)
	for name, p := range map[string]string{"solid version": solid, "solid resource": solidRes, "not a WIM": junk} {
		if _, err := Split(p, filepath.Join(t.TempDir(), "install.swm"), 1<<20, nil); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	good, _ := buildWIM(t, 1, [][]byte{{1}}, versionDefault, 0)
	if _, err := Split(good, filepath.Join(t.TempDir(), "install.wim2"), 1<<20, nil); err == nil {
		t.Error("a part name without .swm accepted")
	}
}
