package flash

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"github.com/uplinkresearch/dsky/internal/device"
)

type darwinTarget struct {
	f *os.File
}

// OpenTarget unmounts the whole disk via diskutil, then opens the raw node
// (/dev/rdiskN — ~10x the throughput of the buffered node).
func OpenTarget(ctx context.Context, dev device.Device) (Target, error) {
	// dev.ID is /dev/rdiskN; diskutil wants the buffered name.
	buffered := strings.Replace(dev.ID, "/dev/rdisk", "/dev/disk", 1)
	if out, err := exec.CommandContext(ctx, "diskutil", "unmountDisk", buffered).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("flash: diskutil unmountDisk %s: %v\n%s", buffered, err, out)
	}
	f, err := openRaw(ctx, dev.ID)
	if err != nil {
		if os.IsPermission(err) {
			return nil, ErrNeedsElevation
		}
		return nil, fmt.Errorf("flash: opening %s: %w", dev.ID, err)
	}
	return &darwinTarget{f: f}, nil
}

// Disk ioctls from <sys/disk.h>: DKIOCGETBLOCKSIZE is _IOR('d', 24, uint32)
// and DKIOCGETBLOCKCOUNT is _IOR('d', 25, uint64).
const (
	dkiocGetBlockSize  = 0x40046418
	dkiocGetBlockCount = 0x40086419
)

func (t *darwinTarget) Size() (int64, error) {
	// A raw disk node can't seek to its end, so ask the disk: block count
	// times block size, as diskutil does. Seeking is kept for anything that
	// isn't a disk.
	fd := int(t.f.Fd())
	var bs uint32
	var count uint64
	if err := ioctl(fd, dkiocGetBlockSize, unsafe.Pointer(&bs)); err == nil && bs > 0 {
		if err := ioctl(fd, dkiocGetBlockCount, unsafe.Pointer(&count)); err == nil && count > 0 {
			return int64(count) * int64(bs), nil
		}
	}
	if n, err := t.f.Seek(0, 2); err == nil && n > 0 {
		if _, err := t.f.Seek(0, 0); err != nil {
			return 0, err
		}
		return n, nil
	}
	return 0, fmt.Errorf("flash: cannot determine size of %s", t.f.Name())
}

func ioctl(fd int, req uintptr, arg unsafe.Pointer) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(arg)); errno != 0 {
		return errno
	}
	return nil
}

func (t *darwinTarget) WriteAt(p []byte, off int64) (int, error) { return t.f.WriteAt(p, off) }
func (t *darwinTarget) ReadAt(p []byte, off int64) (int, error)  { return t.f.ReadAt(p, off) }
func (t *darwinTarget) Sync() error {
	// Go's Sync on macOS is F_FULLFSYNC, which a raw disk node (/dev/rdiskN)
	// doesn't support ("inappropriate ioctl for device"). Writes to a raw node
	// go straight to the device unbuffered, so there is nothing to flush —
	// but the error failed every Mac write at the very end, after the image
	// had been written. The readback verify that follows is the real check.
	if err := t.f.Sync(); err != nil && !errors.Is(err, syscall.ENOTTY) && !errors.Is(err, syscall.EINVAL) {
		return err
	}
	return nil
}
func (t *darwinTarget) Finalize() error                          { return nil }
func (t *darwinTarget) Close() error                             { return t.f.Close() }
