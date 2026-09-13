package flash

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/uplinkresearch/bootwright/internal/device"
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
	f, err := os.OpenFile(dev.ID, os.O_RDWR, 0)
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
func (t *darwinTarget) Sync() error                              { return t.f.Sync() }
func (t *darwinTarget) Finalize() error                          { return nil }
func (t *darwinTarget) Close() error                             { return t.f.Close() }
