// Package device enumerates candidate flash targets. The listing itself is
// the first safety gate: only removable USB-attached disks are ever
// returned as flashable, and the disk hosting the running OS is marked so
// every later layer can refuse it.
package device

import (
	"context"
	"fmt"
)

// Device is one physical disk.
type Device struct {
	// ID is the platform path handed to the flash engine:
	// \\.\PhysicalDriveN, /dev/rdiskN, /dev/sdX.
	ID        string
	Index     int // Windows disk number; -1 elsewhere
	Model     string
	Serial    string
	SizeBytes int64
	Bus       string // "usb", "nvme", ...
	Removable bool
	System    bool     // hosts the running OS — never flashable
	Mounts    []string // mounted volumes/drive letters, informational
}

// Flashable is the single policy gate: USB-attached, removable-class, not
// the system disk.
func (d Device) Flashable() bool {
	return d.Bus == "usb" && !d.System
}

// String renders one listing row.
func (d Device) String() string {
	size := float64(d.SizeBytes) / (1 << 30)
	note := ""
	if d.System {
		note = "  [SYSTEM DISK — never flashable]"
	} else if !d.Flashable() {
		note = "  [not usb — not flashable]"
	}
	return fmt.Sprintf("%-22s %7.1f GiB  %-4s  %s%s", d.ID, size, d.Bus, d.Model, note)
}

// List enumerates disks, most-plausible flash targets first.
func List(ctx context.Context) ([]Device, error) {
	return list(ctx)
}

// SizeConfirmation is the typed string a caller must supply to arm a flash
// — the exact size in GiB with one decimal, e.g. "14.9". Ported from
// make-nuc-usb.sh's "type the SIZE exactly as shown" interlock.
func (d Device) SizeConfirmation() string {
	return fmt.Sprintf("%.1f", float64(d.SizeBytes)/(1<<30))
}
