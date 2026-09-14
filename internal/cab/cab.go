// Package cab extracts Microsoft cabinet files compressed with MSZIP, or not
// compressed at all, without an external tool.
//
// Dell and HP publish their driver-pack catalogs as MSZIP cabinets. Expanding
// them used to need expand.exe on Windows and 7-Zip everywhere else, so on a
// Mac or Linux machine without 7-Zip those two vendors silently vanished from
// the model list. MSZIP is deflate in blocks, which the standard library
// already reads. LZX and Quantum cabinets (some driver packs) are not handled
// here; Extract reports ErrUnsupported so the caller can fall back to a tool.
package cab

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnsupported means the cabinet uses a compression this package can't read,
// or spans several cabinet files.
var ErrUnsupported = errors.New("cabinet format not supported")

const (
	flagPrev    = 0x0001
	flagNext    = 0x0002
	flagReserve = 0x0004

	compNone  = 0
	compMSZIP = 1
	compMask  = 0x000f

	maxBlock = 32768
)

type folder struct {
	dataOffset uint32
	blocks     uint16
	comp       uint16
}

type file struct {
	size   uint32
	offset uint32 // within the folder's uncompressed stream
	folder uint16
	name   string
}

// Extract writes every file in the cabinet at path into dir and returns the
// paths written.
func Extract(path, dir string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return extract(b, dir)
}

func extract(b []byte, dir string) ([]string, error) {
	if len(b) < 36 || string(b[:4]) != "MSCF" {
		return nil, fmt.Errorf("not a cabinet file")
	}
	le := binary.LittleEndian
	coffFiles := le.Uint32(b[16:])
	nFolders := int(le.Uint16(b[26:]))
	nFiles := int(le.Uint16(b[28:]))
	flags := le.Uint16(b[30:])
	if flags&(flagPrev|flagNext) != 0 {
		return nil, fmt.Errorf("%w: spans several cabinets", ErrUnsupported)
	}
	pos := 36
	var folderReserve, dataReserve int
	if flags&flagReserve != 0 {
		if len(b) < pos+4 {
			return nil, errTruncated
		}
		headerReserve := int(le.Uint16(b[pos:]))
		folderReserve = int(b[pos+2])
		dataReserve = int(b[pos+3])
		pos += 4 + headerReserve
	}

	folders := make([]folder, nFolders)
	for i := range folders {
		if len(b) < pos+8 {
			return nil, errTruncated
		}
		folders[i] = folder{le.Uint32(b[pos:]), le.Uint16(b[pos+4:]), le.Uint16(b[pos+6:])}
		pos += 8 + folderReserve
	}

	files := make([]file, 0, nFiles)
	pos = int(coffFiles)
	for i := 0; i < nFiles; i++ {
		if len(b) < pos+16 {
			return nil, errTruncated
		}
		f := file{size: le.Uint32(b[pos:]), offset: le.Uint32(b[pos+4:]), folder: le.Uint16(b[pos+8:])}
		end := bytes.IndexByte(b[pos+16:], 0)
		if end < 0 {
			return nil, errTruncated
		}
		f.name = string(b[pos+16 : pos+16+end])
		pos += 16 + end + 1
		files = append(files, f)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	streams := map[uint16][]byte{}
	var written []string
	for _, f := range files {
		if int(f.folder) >= len(folders) {
			// 0xFFFD..0xFFFF continue a file from or into another cabinet.
			return nil, fmt.Errorf("%w: file %q continues across cabinets", ErrUnsupported, f.name)
		}
		data, ok := streams[f.folder]
		if !ok {
			var err error
			if data, err = folderData(b, folders[f.folder], dataReserve); err != nil {
				return nil, err
			}
			streams[f.folder] = data
		}
		if uint64(f.offset)+uint64(f.size) > uint64(len(data)) {
			return nil, fmt.Errorf("file %q runs past the end of its folder", f.name)
		}
		out, err := safeJoin(dir, f.name)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(out, data[f.offset:f.offset+f.size], 0o644); err != nil {
			return nil, err
		}
		written = append(written, out)
	}
	return written, nil
}

var errTruncated = errors.New("cabinet file is truncated")

// folderData decompresses one folder's data blocks into its whole stream.
func folderData(b []byte, fo folder, dataReserve int) ([]byte, error) {
	comp := fo.comp & compMask
	if comp != compNone && comp != compMSZIP {
		return nil, fmt.Errorf("%w: compression type %d", ErrUnsupported, comp)
	}
	le := binary.LittleEndian
	pos := int(fo.dataOffset)
	var out []byte
	var prev []byte
	for i := 0; i < int(fo.blocks); i++ {
		if len(b) < pos+8 {
			return nil, errTruncated
		}
		cbData := int(le.Uint16(b[pos+4:]))
		cbUncomp := int(le.Uint16(b[pos+6:]))
		pos += 8 + dataReserve
		if len(b) < pos+cbData {
			return nil, errTruncated
		}
		block := b[pos : pos+cbData]
		pos += cbData
		if cbUncomp > maxBlock {
			return nil, fmt.Errorf("data block %d claims %d bytes, over the %d limit", i, cbUncomp, maxBlock)
		}
		if comp == compNone {
			out = append(out, block...)
			continue
		}
		// Each MSZIP block is "CK" and a deflate stream whose back-references
		// may reach into the previous block's output.
		if len(block) < 2 || block[0] != 'C' || block[1] != 'K' {
			return nil, fmt.Errorf("data block %d is not MSZIP", i)
		}
		r := flate.NewReaderDict(bytes.NewReader(block[2:]), prev)
		buf := make([]byte, cbUncomp)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, fmt.Errorf("data block %d: %w", i, err)
		}
		r.Close()
		out = append(out, buf...)
		prev = buf
	}
	return out, nil
}

// safeJoin keeps a stored name inside dir: cabinet names use backslashes and
// could otherwise climb out with "..".
func safeJoin(dir, name string) (string, error) {
	name = strings.ReplaceAll(name, `\`, "/")
	clean := filepath.Clean("/" + name)
	if clean == "/" {
		return "", fmt.Errorf("cabinet holds a file with an empty name")
	}
	return filepath.Join(dir, filepath.FromSlash(clean[1:])), nil
}
