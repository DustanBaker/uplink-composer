// Package stream opens artifact/blob files with transparent xz, zstd, gzip,
// or bzip2 decompression, shared by the flash engine and the CLI.
package stream

import (
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"
	"os"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// Detect sniffs xz/zstd/gzip/bzip2 magic bytes; returns "none" otherwise.
func Detect(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var magic [6]byte
	n, err := f.Read(magic[:])
	if err != nil || n < 4 {
		return "", fmt.Errorf("reading magic of %s: %w", path, err)
	}
	switch {
	case magic[0] == 0xFD && magic[1] == '7' && magic[2] == 'z' && magic[3] == 'X' && magic[4] == 'Z':
		return "xz", nil
	case magic[0] == 0x28 && magic[1] == 0xB5 && magic[2] == 0x2F && magic[3] == 0xFD:
		return "zstd", nil
	case magic[0] == 0x1F && magic[1] == 0x8B:
		return "gz", nil
	// "BZh" then the block-size digit: Valve ships the Steam Deck recovery
	// image this way.
	case magic[0] == 'B' && magic[1] == 'Z' && magic[2] == 'h' && magic[3] >= '1' && magic[3] <= '9':
		return "bz2", nil
	default:
		return "none", nil
	}
}

type readCloser struct {
	io.Reader
	closers []io.Closer
}

func (r *readCloser) Close() error {
	var first error
	for _, c := range r.closers {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Open returns a reader over path decoded per compress ("none", "xz",
// "zstd", "gz", "bz2"). Callers get the uncompressed stream; the size is
// unknown for compressed inputs.
func Open(path, compress string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	switch compress {
	case "", "none":
		return f, nil
	case "xz":
		xr, err := xz.NewReader(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("opening xz stream: %w", err)
		}
		return &readCloser{Reader: xr, closers: []io.Closer{f}}, nil
	case "zstd":
		zr, err := zstd.NewReader(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("opening zstd stream: %w", err)
		}
		return &readCloser{Reader: zr, closers: []io.Closer{zr.IOReadCloser(), f}}, nil
	case "gz":
		gr, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("opening gzip stream: %w", err)
		}
		return &readCloser{Reader: gr, closers: []io.Closer{gr, f}}, nil
	case "bz2":
		// compress/bzip2 is decompress-only, which is all a flasher needs,
		// and returns a plain Reader with nothing of its own to close.
		return &readCloser{Reader: bzip2.NewReader(f), closers: []io.Closer{f}}, nil
	default:
		f.Close()
		return nil, fmt.Errorf("unknown compression %q", compress)
	}
}
