//go:build windows

package flash

import (
	"fmt"
	"io"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Probe reports which writes Windows will accept on a raw disk, so a stick
// that refuses them can be diagnosed on the machine where it happens instead
// of guessed at from a distance.
//
// It writes only inside the last two megabytes -- the same bytes DSKY zeroes
// before writing an image, and nothing a filesystem keeps anything in -- and
// only after the caller has named the disk's exact size, so it cannot be
// pointed at the wrong one by accident.
func Probe(device string, wantSize int64, out io.Writer) error {
	say := func(f string, a ...any) { fmt.Fprintf(out, f+"\n", a...) }
	say("probing %s", device)

	for _, mode := range []struct {
		name  string
		flags uint32
	}{
		{"write-through, buffered (what DSKY uses now)", windows.FILE_FLAG_WRITE_THROUGH},
		{"write-through, unbuffered (what DSKY used to use)", windows.FILE_FLAG_NO_BUFFERING | windows.FILE_FLAG_WRITE_THROUGH},
		{"no flags", 0},
	} {
		h, err := windows.CreateFile(windows.StringToUTF16Ptr(device),
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
			nil, windows.OPEN_EXISTING, mode.flags, 0)
		if err != nil {
			say("%s: cannot open: %v", mode.name, err)
			continue
		}
		var size int64
		var ret uint32
		if err := windows.DeviceIoControl(h, ioctlDiskGetLengthInfo, nil, 0,
			(*byte)(unsafe.Pointer(&size)), 8, &ret, nil); err != nil {
			say("%s: cannot read its size: %v", mode.name, err)
			windows.CloseHandle(h)
			continue
		}
		if size != wantSize {
			say("%s: this disk is %d bytes, you said %d -- not touching it", mode.name, size, wantSize)
			windows.CloseHandle(h)
			continue
		}
		say("%s: %d bytes", mode.name, size)

		f := os.NewFile(uintptr(h), device)
		for _, n := range []int{4 << 20, 1 << 20, 64 << 10, 4096, 512} {
			off := size - (2 << 20)
			if int64(n) > 2<<20 {
				off = size - int64(n) // a 4 MiB test needs 4 MiB of room
			}
			_, rerr := f.ReadAt(make([]byte, n), off)
			say("  read  %8d at %14d: %s", n, off, result(rerr))

			_, werr := f.WriteAt(make([]byte, n), off)
			say("  write %8d at %14d: %s  (ordinary memory)", n, off, result(werr))

			_, aw := writeDirect(h, alignedBuf(n), off)
			say("  write %8d at %14d: %s  (page-aligned memory)", n, off, result(aw))
		}
		f.Close()
	}
	say("done")
	return nil
}

// alignedBuf returns a buffer whose first byte sits on a 4096-byte boundary,
// which satisfies every sector size Windows asks about. Over-allocating and
// slicing forward does it without taking an address Go would rather keep to
// itself.
func alignedBuf(n int) []byte {
	raw := make([]byte, n+4096)
	skip := (4096 - int(uintptr(unsafe.Pointer(&raw[0]))%4096)) % 4096
	return raw[skip : skip+n]
}

// writeDirect goes straight to WriteFile, so a write that moves no bytes is
// reported as exactly that rather than as something further up the stack.
func writeDirect(h windows.Handle, b []byte, off int64) (uint32, error) {
	ov := windows.Overlapped{Offset: uint32(off), OffsetHigh: uint32(off >> 32)}
	var done uint32
	err := windows.WriteFile(h, b, &done, &ov)
	if err == nil && int(done) != len(b) {
		return done, fmt.Errorf("reported success having written %d of %d bytes", done, len(b))
	}
	return done, err
}

func result(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}
