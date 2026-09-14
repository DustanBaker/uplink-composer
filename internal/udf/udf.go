// Package udf reads the UDF filesystem on optical-disc images, which is how
// Windows install ISOs store their files, so DSKY can extract a Windows ISO
// without 7-Zip.
//
// Windows ISOs carry a small ISO 9660 filesystem too, but it holds only a
// README telling you the real one is UDF. Microsoft's ISOs are UDF 1.02; this
// reads UDF 1.02 through 2.01 as a read-only, unfragmented-metadata reader:
// one physical partition (type 1 partition map), file entries and extended
// file entries, short, long and embedded allocation descriptors, and
// allocation extent continuations. Virtual (VAT), sparable and metadata
// partitions, which rewritable discs and UDF 2.50+ use, are refused rather
// than misread.
//
// Written from ECMA-167 (3rd edition) and OSTA UDF 2.01.
package udf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// ErrNotUDF means the image has no UDF volume this package can read.
var ErrNotUDF = errors.New("udf: no UDF filesystem")

const sectorSize = 2048

// Descriptor tag identifiers (ECMA-167 3/7.2.1, 4/7.2.1).
const (
	tagPrimaryVolume   = 1
	tagAnchor          = 2
	tagPartition       = 5
	tagLogicalVolume   = 6
	tagTerminating     = 8
	tagFileSet         = 256
	tagFileIdentifier  = 257
	tagAllocExtent     = 258
	tagFileEntry       = 261
	tagExtendedFileEnt = 266
)

// File types in an ICB tag (ECMA-167 4/14.6.6).
const (
	fileTypeDirectory = 4
	fileTypeFile      = 5
)

// FS is an opened UDF volume.
type FS struct {
	r          io.ReaderAt
	blockSize  int64
	partStart  int64 // sector where the partition begins
	partLength int64 // in blocks
	partNumber uint16
	root       longAD
}

// Entry is one file or directory.
type Entry struct {
	Path  string // slash-separated, no leading slash
	Dir   bool
	Size  int64
	fs    *FS
	exts  []extent // file contents, in order
	inICB []byte   // embedded data, when the file lives inside its entry
}

type extent struct {
	offset int64 // byte offset in the image; -1 for a hole that reads as zeros
	length int64
}

type longAD struct {
	length    uint32
	block     uint32
	partition uint16
}

// Open reads the volume structure from an image.
func Open(r io.ReaderAt) (*FS, error) {
	fs := &FS{r: r, blockSize: sectorSize}
	anchor := make([]byte, sectorSize)
	if _, err := r.ReadAt(anchor, 256*sectorSize); err != nil {
		return nil, fmt.Errorf("%w: reading the anchor: %v", ErrNotUDF, err)
	}
	if tagID(anchor) != tagAnchor {
		return nil, ErrNotUDF
	}
	vdsLen := binary.LittleEndian.Uint32(anchor[16:])
	vdsLoc := binary.LittleEndian.Uint32(anchor[20:])

	var (
		haveLVD, havePD bool
		fsd             longAD
		mapPartition    uint16
	)
	buf := make([]byte, sectorSize)
	for i := uint32(0); i < vdsLen/sectorSize; i++ {
		if _, err := r.ReadAt(buf, int64(vdsLoc+i)*sectorSize); err != nil {
			return nil, fmt.Errorf("udf: reading the volume descriptors: %w", err)
		}
		switch tagID(buf) {
		case tagPartition:
			fs.partNumber = binary.LittleEndian.Uint16(buf[22:])
			fs.partStart = int64(binary.LittleEndian.Uint32(buf[188:]))
			fs.partLength = int64(binary.LittleEndian.Uint32(buf[192:]))
			havePD = true
		case tagLogicalVolume:
			bs := binary.LittleEndian.Uint32(buf[212:])
			if bs != sectorSize {
				return nil, fmt.Errorf("udf: logical block size %d is not supported", bs)
			}
			fsd = parseLongAD(buf[248:])
			nMaps := binary.LittleEndian.Uint32(buf[268:])
			maps := buf[440:]
			if nMaps != 1 || len(maps) < 6 || maps[0] != 1 {
				return nil, fmt.Errorf("udf: only a single physical partition is supported (this volume has %d maps, first of type %d)", nMaps, maps[0])
			}
			mapPartition = binary.LittleEndian.Uint16(maps[4:])
			haveLVD = true
		}
		if tagID(buf) == tagTerminating {
			break
		}
	}
	if !haveLVD || !havePD {
		return nil, fmt.Errorf("%w: missing partition or logical volume descriptor", ErrNotUDF)
	}
	if mapPartition != fs.partNumber {
		return nil, fmt.Errorf("udf: the partition map names partition %d, but the volume has partition %d", mapPartition, fs.partNumber)
	}

	fsdBuf := make([]byte, sectorSize)
	if _, err := r.ReadAt(fsdBuf, fs.blockOffset(fsd.block)); err != nil {
		return nil, fmt.Errorf("udf: reading the file set descriptor: %w", err)
	}
	if tagID(fsdBuf) != tagFileSet {
		return nil, fmt.Errorf("udf: no file set descriptor at block %d", fsd.block)
	}
	fs.root = parseLongAD(fsdBuf[400:])
	return fs, nil
}

func (fs *FS) blockOffset(block uint32) int64 {
	return (fs.partStart + int64(block)) * fs.blockSize
}

func tagID(b []byte) uint16 { return binary.LittleEndian.Uint16(b[0:]) }

func parseLongAD(b []byte) longAD {
	return longAD{
		length:    binary.LittleEndian.Uint32(b[0:]),
		block:     binary.LittleEndian.Uint32(b[4:]),
		partition: binary.LittleEndian.Uint16(b[8:]),
	}
}

// Walk calls fn for every file and directory, parents before children.
func (fs *FS) Walk(fn func(Entry) error) error {
	root, err := fs.readEntry(fs.root, "")
	if err != nil {
		return err
	}
	return fs.walk(root, fn, 0)
}

func (fs *FS) walk(dir Entry, fn func(Entry) error, depth int) error {
	if depth > 64 {
		return fmt.Errorf("udf: directories nest deeper than 64 at %s — refusing a loop", dir.Path)
	}
	data, err := io.ReadAll(dir.Open())
	if err != nil {
		return fmt.Errorf("udf: reading directory %s: %w", dir.Path, err)
	}
	for pos := 0; pos+38 <= len(data); {
		if tagID(data[pos:]) != tagFileIdentifier {
			return fmt.Errorf("udf: directory %s is damaged at byte %d", dir.Path, pos)
		}
		chars := data[pos+18]
		lenFI := int(data[pos+19])
		icb := parseLongAD(data[pos+20:])
		lenIU := int(binary.LittleEndian.Uint16(data[pos+36:]))
		nameStart := pos + 38 + lenIU
		if nameStart+lenFI > len(data) {
			return fmt.Errorf("udf: directory %s has an entry running past its end", dir.Path)
		}
		name := decodeName(data[nameStart : nameStart+lenFI])
		pos += (38 + lenIU + lenFI + 3) &^ 3

		const deleted, parent = 1 << 2, 1 << 3
		if chars&(deleted|parent) != 0 || name == "" {
			continue
		}
		if name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
			return fmt.Errorf("udf: directory %s has an entry named %q", dir.Path, name)
		}
		child, err := fs.readEntry(icb, path.Join(dir.Path, name))
		if err != nil {
			return err
		}
		if err := fn(child); err != nil {
			return err
		}
		if child.Dir {
			if err := fs.walk(child, fn, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// readEntry reads a file entry and the extents that hold its data.
func (fs *FS) readEntry(icb longAD, p string) (Entry, error) {
	buf := make([]byte, fs.blockSize)
	if _, err := fs.r.ReadAt(buf, fs.blockOffset(icb.block)); err != nil {
		return Entry{}, fmt.Errorf("udf: reading the entry for %s: %w", p, err)
	}
	var lenEA, lenAD uint32
	var adStart int
	switch tagID(buf) {
	case tagFileEntry:
		lenEA = binary.LittleEndian.Uint32(buf[168:])
		lenAD = binary.LittleEndian.Uint32(buf[172:])
		adStart = 176
	case tagExtendedFileEnt:
		lenEA = binary.LittleEndian.Uint32(buf[208:])
		lenAD = binary.LittleEndian.Uint32(buf[212:])
		adStart = 216
	default:
		return Entry{}, fmt.Errorf("udf: %s has no file entry at block %d (tag %d)", p, icb.block, tagID(buf))
	}
	e := Entry{Path: p, fs: fs}
	fileType := buf[16+11]
	e.Dir = fileType == fileTypeDirectory
	if !e.Dir && fileType != fileTypeFile {
		return Entry{}, fmt.Errorf("udf: %s is file type %d, which is not supported", p, fileType)
	}
	e.Size = int64(binary.LittleEndian.Uint64(buf[56:]))
	start := adStart + int(lenEA)
	if start+int(lenAD) > len(buf) {
		return Entry{}, fmt.Errorf("udf: the entry for %s is damaged", p)
	}
	ads := buf[start : start+int(lenAD)]
	flags := binary.LittleEndian.Uint16(buf[16+18:])
	switch flags & 7 {
	case 3: // data embedded in the entry itself
		if int64(len(ads)) < e.Size {
			return Entry{}, fmt.Errorf("udf: %s claims %d embedded bytes but holds %d", p, e.Size, len(ads))
		}
		e.inICB = append([]byte(nil), ads[:e.Size]...)
		return e, nil
	case 0, 1:
		exts, err := fs.extents(ads, flags&7 == 1, p)
		if err != nil {
			return Entry{}, err
		}
		e.exts = exts
	default:
		return Entry{}, fmt.Errorf("udf: %s uses extended allocation descriptors, which are not supported", p)
	}
	var total int64
	for _, x := range e.exts {
		total += x.length
	}
	if total < e.Size {
		return Entry{}, fmt.Errorf("udf: %s is %d bytes but its extents hold %d", p, e.Size, total)
	}
	return e, nil
}

// extents decodes allocation descriptors, following continuation extents.
func (fs *FS) extents(ads []byte, long bool, p string) ([]extent, error) {
	size := 8
	if long {
		size = 16
	}
	var out []extent
	for hops := 0; ; hops++ {
		if hops > 1024 {
			return nil, fmt.Errorf("udf: %s has too many allocation extents", p)
		}
		next := []byte(nil)
		for i := 0; i+size <= len(ads); i += size {
			raw := binary.LittleEndian.Uint32(ads[i:])
			length := int64(raw & 0x3FFFFFFF)
			kind := raw >> 30
			block := binary.LittleEndian.Uint32(ads[i+4:])
			if long && binary.LittleEndian.Uint16(ads[i+8:]) != 0 {
				return nil, fmt.Errorf("udf: %s refers to another partition", p)
			}
			if length == 0 {
				break
			}
			switch kind {
			case 0: // recorded
				off := fs.blockOffset(block)
				if block >= uint32(fs.partLength) {
					return nil, fmt.Errorf("udf: %s points past the end of the partition", p)
				}
				out = append(out, extent{offset: off, length: length})
			case 1, 2: // allocated or unallocated, unrecorded: zeros
				out = append(out, extent{offset: -1, length: length})
			case 3: // the rest of the descriptors continue in another extent
				next = make([]byte, length)
				if _, err := fs.r.ReadAt(next, fs.blockOffset(block)); err != nil {
					return nil, fmt.Errorf("udf: reading allocation extent for %s: %w", p, err)
				}
				if tagID(next) != tagAllocExtent {
					return nil, fmt.Errorf("udf: %s has a damaged allocation extent", p)
				}
				l := int(binary.LittleEndian.Uint32(next[20:]))
				if 24+l > len(next) {
					return nil, fmt.Errorf("udf: %s has a damaged allocation extent", p)
				}
				next = next[24 : 24+l]
			}
			if kind == 3 {
				break
			}
		}
		if next == nil {
			return out, nil
		}
		ads = next
	}
}

// Open returns the file's contents.
func (e Entry) Open() io.Reader {
	if e.inICB != nil || (len(e.exts) == 0 && e.Size == 0) {
		return strings.NewReader(string(e.inICB))
	}
	readers := make([]io.Reader, 0, len(e.exts))
	remaining := e.Size
	for _, x := range e.exts {
		if remaining <= 0 {
			break
		}
		n := min(x.length, remaining)
		if x.offset < 0 {
			readers = append(readers, io.LimitReader(zeros{}, n))
		} else {
			readers = append(readers, io.NewSectionReader(e.fs.r, x.offset, n))
		}
		remaining -= n
	}
	return io.MultiReader(readers...)
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// decodeName decodes an OSTA CS0 d-string: a compression id of 8 (one byte
// per character) or 16 (UTF-16BE), then the characters.
func decodeName(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	switch b[0] {
	case 8:
		r := make([]rune, 0, len(b)-1)
		for _, c := range b[1:] {
			r = append(r, rune(c))
		}
		return string(r)
	case 16:
		u := make([]uint16, 0, (len(b)-1)/2)
		for i := 1; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
		}
		return string(utf16.Decode(u))
	}
	return ""
}

// Extract writes every file in the volume under dir. progress, if not nil,
// hears bytes written against the volume's total file size.
func (fs *FS) Extract(dir string, progress func(done, total int64)) error {
	var entries []Entry
	var total int64
	if err := fs.Walk(func(e Entry) error {
		entries = append(entries, e)
		if !e.Dir {
			total += e.Size
		}
		return nil
	}); err != nil {
		return err
	}
	var done int64
	buf := make([]byte, 4<<20)
	for _, e := range entries {
		dest, err := safeJoin(dir, e.Path)
		if err != nil {
			return err
		}
		if e.Dir {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		out, err := os.Create(dest)
		if err != nil {
			return err
		}
		n, err := io.CopyBuffer(&countWriter{w: out, add: func(k int) {
			done += int64(k)
			if progress != nil {
				progress(done, total)
			}
		}}, e.Open(), buf)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("udf: extracting %s: %w", e.Path, err)
		}
		if n != e.Size {
			return fmt.Errorf("udf: extracting %s: wrote %d of %d bytes", e.Path, n, e.Size)
		}
	}
	return nil
}

type countWriter struct {
	w   io.Writer
	add func(int)
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.add(n)
	return n, err
}

// safeJoin keeps a name from the image inside dir.
func safeJoin(dir, name string) (string, error) {
	clean := path.Clean("/" + name)
	if clean == "/" {
		return "", fmt.Errorf("udf: an entry has an empty name")
	}
	return filepath.Join(dir, filepath.FromSlash(clean[1:])), nil
}

// ExtractFile opens an image file and extracts its UDF volume under dir.
func ExtractFile(image, dir string, progress func(done, total int64)) error {
	f, err := os.Open(image)
	if err != nil {
		return err
	}
	defer f.Close()
	fs, err := Open(f)
	if err != nil {
		return err
	}
	return fs.Extract(dir, progress)
}
