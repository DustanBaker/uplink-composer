package flash

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/DustanBaker/uplink-composer/internal/device"
)

const (
	fsctlLockVolume          = 0x00090018
	fsctlDismountVolume      = 0x00090020
	ioctlDiskGetLengthInfo   = 0x0007405C
	ioctlDiskUpdateProperties = 0x00070140
	ioctlVolumeGetExtents    = 0x00560000
	ioctlStorageEjectMedia   = 0x002D4808
)

type winTarget struct {
	f       *os.File
	handle  windows.Handle
	volumes []windows.Handle // locked+dismounted volume handles, held open
}

// OpenTarget opens \\.\PhysicalDriveN unbuffered/write-through, then locks
// and dismounts every volume on that disk (which also suppresses Explorer's
// "format this disk?" popups mid-write). Requires administrator rights —
// callers get ErrNeedsElevation to trigger the elevated worker.
func OpenTarget(ctx context.Context, dev device.Device) (Target, error) {
	h, err := windows.CreateFile(
		windows.StringToUTF16Ptr(dev.ID),
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_NO_BUFFERING|windows.FILE_FLAG_WRITE_THROUGH, 0)
	if err != nil {
		if err == windows.ERROR_ACCESS_DENIED {
			return nil, ErrNeedsElevation
		}
		return nil, fmt.Errorf("flash: opening %s: %w", dev.ID, err)
	}
	t := &winTarget{handle: h, f: os.NewFile(uintptr(h), dev.ID)}
	if err := t.lockVolumes(ctx, dev.Index); err != nil {
		t.Close()
		return nil, err
	}
	return t, nil
}

// lockVolumes finds every volume with an extent on disk index and takes
// FSCTL_LOCK_VOLUME + FSCTL_DISMOUNT_VOLUME on it.
func (t *winTarget) lockVolumes(ctx context.Context, diskIndex int) error {
	nameBuf := make([]uint16, windows.MAX_PATH+1)
	fh, err := windows.FindFirstVolume(&nameBuf[0], uint32(len(nameBuf)))
	if err != nil {
		return fmt.Errorf("flash: enumerating volumes: %w", err)
	}
	defer windows.FindVolumeClose(fh)
	for {
		volPath := strings.TrimSuffix(windows.UTF16ToString(nameBuf), `\`)
		if err := t.maybeLockVolume(ctx, volPath, diskIndex); err != nil {
			return err
		}
		if err := windows.FindNextVolume(fh, &nameBuf[0], uint32(len(nameBuf))); err != nil {
			if err == windows.ERROR_NO_MORE_FILES {
				return nil
			}
			return fmt.Errorf("flash: enumerating volumes: %w", err)
		}
	}
}

type winDiskExtent struct {
	DiskNumber   uint32
	_            uint32
	StartingByte int64
	ExtentLength int64
}

type winVolumeExtents struct {
	Count   uint32
	_       uint32
	Extents [8]winDiskExtent
}

func (t *winTarget) maybeLockVolume(ctx context.Context, volPath string, diskIndex int) error {
	vh, err := windows.CreateFile(
		windows.StringToUTF16Ptr(volPath),
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil // volume not openable (e.g. no medium) — not ours
	}
	var ext winVolumeExtents
	var ret uint32
	if err := windows.DeviceIoControl(vh, ioctlVolumeGetExtents,
		nil, 0, (*byte)(unsafe.Pointer(&ext)), uint32(unsafe.Sizeof(ext)), &ret, nil); err != nil {
		windows.CloseHandle(vh)
		return nil
	}
	ours := false
	for i := uint32(0); i < ext.Count && i < 8; i++ {
		if int(ext.Extents[i].DiskNumber) == diskIndex {
			ours = true
		}
	}
	if !ours {
		windows.CloseHandle(vh)
		return nil
	}
	// Lock with retries — indexers/AV hold transient handles.
	var lockErr error
	for attempt := 0; attempt < 10; attempt++ {
		if err := ctx.Err(); err != nil {
			windows.CloseHandle(vh)
			return err
		}
		lockErr = windows.DeviceIoControl(vh, fsctlLockVolume, nil, 0, nil, 0, &ret, nil)
		if lockErr == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if lockErr != nil {
		windows.CloseHandle(vh)
		return fmt.Errorf("flash: cannot lock volume %s on target disk — close any Explorer windows or programs using the stick and retry: %w", volPath, lockErr)
	}
	if err := windows.DeviceIoControl(vh, fsctlDismountVolume, nil, 0, nil, 0, &ret, nil); err != nil {
		windows.CloseHandle(vh)
		return fmt.Errorf("flash: dismounting volume %s: %w", volPath, err)
	}
	t.volumes = append(t.volumes, vh)
	return nil
}

func (t *winTarget) Size() (int64, error) {
	var length int64
	var ret uint32
	err := windows.DeviceIoControl(t.handle, ioctlDiskGetLengthInfo,
		nil, 0, (*byte)(unsafe.Pointer(&length)), 8, &ret, nil)
	if err != nil {
		return 0, fmt.Errorf("flash: querying device size: %w", err)
	}
	return length, nil
}

func (t *winTarget) WriteAt(p []byte, off int64) (int, error) { return t.f.WriteAt(p, off) }
func (t *winTarget) ReadAt(p []byte, off int64) (int, error)  { return t.f.ReadAt(p, off) }
func (t *winTarget) Sync() error                              { return windows.FlushFileBuffers(t.handle) }

func (t *winTarget) Finalize() error {
	var ret uint32
	// Refresh the kernel's cached partition table; ignore eject failures.
	err := windows.DeviceIoControl(t.handle, ioctlDiskUpdateProperties, nil, 0, nil, 0, &ret, nil)
	return err
}

func (t *winTarget) Close() error {
	for _, vh := range t.volumes {
		windows.CloseHandle(vh) // releases the lock
	}
	t.volumes = nil
	return t.f.Close() // closes t.handle
}
