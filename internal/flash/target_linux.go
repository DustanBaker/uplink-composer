package flash

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/DustanBaker/the-composer/internal/device"
)

type unixTarget struct {
	f *os.File
}

// OpenTarget unmounts every mounted partition of the device, then opens it
// O_RDWR|O_EXCL — the kernel refuses O_EXCL while anything on the disk is
// still mounted, a free interlock.
func OpenTarget(ctx context.Context, dev device.Device) (Target, error) {
	if err := unmountAll(ctx, dev.ID); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(dev.ID, os.O_RDWR|unix.O_EXCL, 0)
	if err != nil {
		if os.IsPermission(err) {
			return nil, ErrNeedsElevation
		}
		return nil, fmt.Errorf("flash: opening %s (still mounted?): %w", dev.ID, err)
	}
	return &unixTarget{f: f}, nil
}

func unmountAll(ctx context.Context, devPath string) error {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || !strings.HasPrefix(fields[0], devPath) {
			continue
		}
		if out, err := exec.CommandContext(ctx, "umount", fields[1]).CombinedOutput(); err != nil {
			return fmt.Errorf("flash: unmounting %s: %v\n%s", fields[1], err, out)
		}
	}
	return sc.Err()
}

func (t *unixTarget) Size() (int64, error) {
	return t.f.Seek(0, 2)
}

func (t *unixTarget) WriteAt(p []byte, off int64) (int, error) { return t.f.WriteAt(p, off) }
func (t *unixTarget) ReadAt(p []byte, off int64) (int, error)  { return t.f.ReadAt(p, off) }
func (t *unixTarget) Sync() error                              { return t.f.Sync() }

func (t *unixTarget) Finalize() error {
	// Re-read the partition table so /dev nodes reflect the new layout.
	_ = unix.IoctlSetInt(int(t.f.Fd()), unix.BLKRRPART, 0)
	return nil
}

func (t *unixTarget) Close() error { return t.f.Close() }
