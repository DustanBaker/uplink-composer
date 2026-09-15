package flash

import (
	"io"
	"testing"
)

// fakeTarget records writes so the tail wipe can be checked without a disk.
type tailTarget struct {
	size   int64
	writes []struct {
		off int64
		n   int
	}
	failLargerThan int
}

func (f *tailTarget) WriteAt(p []byte, off int64) (int, error) {
	if f.failLargerThan > 0 && len(p) > f.failLargerThan {
		return 0, io.ErrUnexpectedEOF
	}
	f.writes = append(f.writes, struct {
		off int64
		n   int
	}{off, len(p)})
	return len(p), nil
}
func (f *tailTarget) ReadAt(p []byte, off int64) (int, error) { return 0, io.EOF }
func (f *tailTarget) Size() (int64, error)                    { return f.size, nil }
func (f *tailTarget) Sync() error                             { return nil }
func (f *tailTarget) Finalize() error                         { return nil }
func (f *tailTarget) Close() error                            { return nil }

// The wipe covers exactly the last tailWipe bytes, in sector-aligned pieces,
// and never writes past the end of the device. A Windows build failed here
// with "unexpected EOF" on a single two-megabyte request.
func TestWipeTailWritesAlignedPiecesInsideTheDevice(t *testing.T) {
	const size = int64(58) << 30
	tt := &tailTarget{size: size}
	if err := wipeTail(tt, size); err != nil {
		t.Fatal(err)
	}
	var covered int64
	for _, w := range tt.writes {
		if w.off%SectorSize != 0 || w.n%SectorSize != 0 {
			t.Errorf("unaligned write: %d bytes at %d", w.n, w.off)
		}
		if w.off < size-tailWipe || w.off+int64(w.n) > size {
			t.Errorf("write outside the tail: %d bytes at %d (device %d)", w.n, w.off, size)
		}
		covered += int64(w.n)
	}
	if covered != tailWipe {
		t.Errorf("wiped %d bytes, want %d", covered, tailWipe)
	}
	if len(tt.writes) < 2 {
		t.Errorf("wrote the whole tail in %d request(s); drivers differ on how much they take at once", len(tt.writes))
	}

	// A device that refuses a large request still gets its tail wiped.
	small := &tailTarget{size: size, failLargerThan: 1 << 20}
	if err := wipeTail(small, size); err != nil {
		t.Errorf("a device refusing requests over 1 MiB: %v", err)
	}
}
