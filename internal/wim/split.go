// Package wim splits a Windows imaging (WIM) file into a spanned set (.swm),
// so an install.wim over FAT32's 4 GiB file limit fits on a stick, without
// wimlib.
//
// Splitting needs no compression code. A WIM stores each file's data as a
// resource, compressed chunk by chunk behind a chunk table whose offsets are
// relative to the resource itself, so a resource can be copied byte for byte
// to any position in any file. A spanned set is then: every part carries a
// header naming its part number and the total, the resources it holds, a
// blob table listing only those resources (each tagged with its part number),
// and the XML description; part 1 also holds the image metadata resources,
// which never span parts.
//
// Solid WIMs (LZMS "solid" resources, which install.esd uses) keep many files
// in one resource and are refused with ErrUnsupported, as are WIMs that are
// already split. The integrity table, optional, is not carried over.
//
// Written from the WIM layout as Microsoft and wimlib document it.
package wim

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrUnsupported means this WIM can't be split here (solid, already split,
// or not a WIM); a caller can fall back to another tool.
var ErrUnsupported = errors.New("wim: not a WIM this splitter handles")

const (
	headerSize    = 208
	reshdrSize    = 24
	blobEntrySize = 50

	flagSpanned         = 0x00000008
	flagWriteInProgress = 0x00000040

	resMetadata   = 0x02
	resCompressed = 0x04
	resSolid      = 0x10

	versionDefault = 0x10d00
)

var magic = []byte("MSWIM\x00\x00\x00")

type reshdr struct {
	size     int64 // size in the WIM (compressed)
	flags    byte
	offset   int64
	origSize int64
}

func readReshdr(b []byte) reshdr {
	var sz [8]byte
	copy(sz[:7], b[:7])
	return reshdr{
		size:     int64(binary.LittleEndian.Uint64(sz[:])),
		flags:    b[7],
		offset:   int64(binary.LittleEndian.Uint64(b[8:])),
		origSize: int64(binary.LittleEndian.Uint64(b[16:])),
	}
}

func (r reshdr) put(b []byte) {
	var sz [8]byte
	binary.LittleEndian.PutUint64(sz[:], uint64(r.size))
	copy(b[:7], sz[:7])
	b[7] = r.flags
	binary.LittleEndian.PutUint64(b[8:], uint64(r.offset))
	binary.LittleEndian.PutUint64(b[16:], uint64(r.origSize))
}

type blob struct {
	res    reshdr
	refcnt uint32
	hash   [20]byte
}

// Header is what Split reads from the source WIM.
type Header struct {
	Version    uint32
	Flags      uint32
	ChunkSize  uint32
	ImageCount uint32
	BootIndex  uint32
	PartNumber uint16
	TotalParts uint16
}

type source struct {
	f     *os.File
	raw   []byte // the 208-byte header as read
	hdr   Header
	table reshdr
	xml   reshdr
	boot  reshdr
	blobs []blob
}

func open(path string) (*source, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	s := &source{f: f, raw: make([]byte, headerSize)}
	if _, err := io.ReadFull(f, s.raw); err != nil {
		f.Close()
		return nil, fmt.Errorf("%w: %s is too short to be a WIM", ErrUnsupported, filepath.Base(path))
	}
	h := s.raw
	if string(h[:8]) != string(magic) || binary.LittleEndian.Uint32(h[8:]) != headerSize {
		f.Close()
		return nil, fmt.Errorf("%w: %s is not a WIM", ErrUnsupported, filepath.Base(path))
	}
	s.hdr = Header{
		Version:    binary.LittleEndian.Uint32(h[12:]),
		Flags:      binary.LittleEndian.Uint32(h[16:]),
		ChunkSize:  binary.LittleEndian.Uint32(h[20:]),
		PartNumber: binary.LittleEndian.Uint16(h[40:]),
		TotalParts: binary.LittleEndian.Uint16(h[42:]),
		ImageCount: binary.LittleEndian.Uint32(h[44:]),
		BootIndex:  binary.LittleEndian.Uint32(h[120:]),
	}
	s.table = readReshdr(h[48:])
	s.xml = readReshdr(h[72:])
	s.boot = readReshdr(h[96:])
	fail := func(format string, a ...any) (*source, error) {
		f.Close()
		return nil, fmt.Errorf("%w: "+format, append([]any{ErrUnsupported}, a...)...)
	}
	switch {
	case s.hdr.Version != versionDefault:
		return fail("WIM version %#x (solid or unknown) is not supported", s.hdr.Version)
	case s.hdr.TotalParts != 1 || s.hdr.PartNumber != 1:
		return fail("the WIM is already part %d of %d", s.hdr.PartNumber, s.hdr.TotalParts)
	case s.hdr.Flags&flagWriteInProgress != 0:
		return fail("the WIM is marked as still being written")
	case s.table.flags&resCompressed != 0 || s.table.size%blobEntrySize != 0:
		return fail("the blob table is compressed or damaged")
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	for _, r := range []reshdr{s.table, s.xml} {
		if r.offset < headerSize || r.offset+r.size > st.Size() {
			return fail("a table points outside the file")
		}
	}
	table := make([]byte, s.table.size)
	if _, err := f.ReadAt(table, s.table.offset); err != nil {
		f.Close()
		return nil, fmt.Errorf("wim: reading the blob table: %w", err)
	}
	for i := 0; i < len(table); i += blobEntrySize {
		e := table[i : i+blobEntrySize]
		b := blob{res: readReshdr(e), refcnt: binary.LittleEndian.Uint32(e[26:])}
		copy(b.hash[:], e[30:50])
		if b.res.flags&resSolid != 0 {
			return fail("the WIM has solid resources")
		}
		if part := binary.LittleEndian.Uint16(e[24:]); part != 1 {
			return fail("a blob belongs to part %d", part)
		}
		if b.res.offset < headerSize || b.res.offset+b.res.size > st.Size() {
			return fail("a resource points outside the file")
		}
		s.blobs = append(s.blobs, b)
	}
	return s, nil
}

// Inspect reads a WIM's header, for callers that want to know before splitting.
func Inspect(path string) (Header, error) {
	s, err := open(path)
	if err != nil {
		return Header{}, err
	}
	s.f.Close()
	return s.hdr, nil
}

// PartNames returns the file names of a spanned set: install.swm,
// install2.swm, install3.swm… — the pattern Windows Setup looks for.
func PartNames(first string, n int) []string {
	ext := filepath.Ext(first)
	base := strings.TrimSuffix(first, ext)
	names := []string{first}
	for i := 2; i <= n; i++ {
		names = append(names, fmt.Sprintf("%s%d%s", base, i, ext))
	}
	return names
}

// Split writes src as a spanned set starting at first (e.g. .../install.swm),
// each part at most maxBytes where a resource allows it, and returns the part
// paths. progress, if not nil, hears bytes copied out of the source's total.
func Split(src, first string, maxBytes int64, progress func(done, total int64)) ([]string, error) {
	if !strings.EqualFold(filepath.Ext(first), ".swm") {
		return nil, fmt.Errorf("wim: split parts must end in .swm, not %s", filepath.Base(first))
	}
	s, err := open(src)
	if err != nil {
		return nil, err
	}
	defer s.f.Close()

	// Metadata first, all in part 1, in table order: the n-th metadata
	// resource is image n. Then the file data in the order it sits in the
	// source, so each part is read and written sequentially.
	var meta, data []int
	for i, b := range s.blobs {
		if b.res.flags&resMetadata != 0 {
			meta = append(meta, i)
		} else {
			data = append(data, i)
		}
	}
	sort.SliceStable(data, func(a, b int) bool { return s.blobs[data[a]].res.offset < s.blobs[data[b]].res.offset })

	fixed := int64(headerSize) + s.xml.size
	var parts [][]int
	cur := append([]int(nil), meta...)
	curSize := fixed + int64(len(cur))*blobEntrySize
	for _, i := range cur {
		curSize += s.blobs[i].res.size
	}
	for _, i := range data {
		add := s.blobs[i].res.size + blobEntrySize
		// A resource never spans parts: one bigger than the limit gets a part
		// to itself, and no part is ever left empty.
		if len(cur) > 0 && curSize+add > maxBytes {
			parts = append(parts, cur)
			cur, curSize = nil, fixed
		}
		cur = append(cur, i)
		curSize += add
	}
	parts = append(parts, cur)
	if len(parts) > 0xFFFF {
		return nil, fmt.Errorf("wim: %d parts is more than a WIM can number", len(parts))
	}

	var total int64
	for _, b := range s.blobs {
		total += b.res.size
	}
	names := PartNames(first, len(parts))
	var done int64
	for p, idx := range parts {
		if err := s.writePart(names[p], p+1, len(parts), idx, func(n int64) {
			done += n
			if progress != nil {
				progress(done, total)
			}
		}); err != nil {
			for _, n := range names {
				os.Remove(n)
			}
			return nil, err
		}
	}
	return names, nil
}

func (s *source) writePart(name string, partNum, totalParts int, idx []int, copied func(int64)) error {
	out, err := os.Create(name)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			out.Close()
		}
	}()
	if _, err := out.Write(make([]byte, headerSize)); err != nil {
		return err
	}
	pos := int64(headerSize)
	table := make([]byte, 0, len(idx)*blobEntrySize)
	newOffset := map[int]int64{}
	buf := make([]byte, 4<<20)
	for _, i := range idx {
		b := s.blobs[i]
		n, err := io.CopyBuffer(out, io.NewSectionReader(s.f, b.res.offset, b.res.size), buf)
		if err != nil {
			return fmt.Errorf("wim: writing %s: %w", filepath.Base(name), err)
		}
		if n != b.res.size {
			return fmt.Errorf("wim: a resource came up %d bytes short", b.res.size-n)
		}
		copied(n)
		newOffset[i] = pos
		e := make([]byte, blobEntrySize)
		r := b.res
		r.offset = pos
		r.put(e)
		binary.LittleEndian.PutUint16(e[24:], uint16(partNum))
		binary.LittleEndian.PutUint32(e[26:], b.refcnt)
		copy(e[30:], b.hash[:])
		table = append(table, e...)
		pos += n
	}

	tableRes := s.table
	tableRes.offset, tableRes.size, tableRes.origSize = pos, int64(len(table)), int64(len(table))
	if _, err := out.Write(table); err != nil {
		return err
	}
	pos += int64(len(table))

	xmlRes := s.xml
	xmlRes.offset = pos
	n, err := io.Copy(out, io.NewSectionReader(s.f, s.xml.offset, s.xml.size))
	if err != nil || n != s.xml.size {
		return fmt.Errorf("wim: copying the XML description: %v", err)
	}

	h := append([]byte(nil), s.raw...)
	flags := (s.hdr.Flags | flagSpanned) &^ flagWriteInProgress
	binary.LittleEndian.PutUint32(h[16:], flags)
	binary.LittleEndian.PutUint16(h[40:], uint16(partNum))
	binary.LittleEndian.PutUint16(h[42:], uint16(totalParts))
	tableRes.put(h[48:])
	xmlRes.put(h[72:])
	// The bootable image's metadata lives in part 1; the other parts don't
	// hold it, so they point at nothing.
	clear(h[96:120])
	if partNum == 1 && s.hdr.BootIndex > 0 {
		nth := 0
		for _, i := range idx {
			if s.blobs[i].res.flags&resMetadata == 0 {
				continue
			}
			nth++
			if nth == int(s.hdr.BootIndex) {
				r := s.blobs[i].res
				r.offset = newOffset[i]
				r.put(h[96:])
			}
		}
	}
	clear(h[124:148]) // no integrity table
	if _, err := out.WriteAt(h, 0); err != nil {
		return err
	}
	ok = true
	return out.Close()
}
