package flash

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

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

func (t *darwinTarget) Size() (int64, error) {
	// Raw character devices don't support seek-to-end reliably; ask the
	// enumerator's cached size via stat fallback.
	if n, err := t.f.Seek(0, 2); err == nil && n > 0 {
		if _, err := t.f.Seek(0, 0); err != nil {
			return 0, err
		}
		return n, nil
	}
	return 0, fmt.Errorf("flash: cannot determine size of %s", t.f.Name())
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
