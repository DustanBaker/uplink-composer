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

// Writable reports whether this disk may be written to at all.
//
// One disk is refused and only one: the disk the running OS is on. That refusal
// is absolute and is not a confirmation anybody can click through, because
// overwriting the filesystem underneath a running kernel does not fail in a way
// the user gets to learn from.
//
// Everything else is somebody's hardware and theirs to overwrite. Whether it
// should be *easy* is a separate question — see Routine.
func (d Device) Writable() bool { return !d.System }

// Routine reports a write that needs no ceremony: a removable USB stick, which
// is what this tool is usually pointed at and the cheapest thing to get wrong.
//
// A fixed disk is still Writable, and deliberately not Routine. The difference
// is what the caller has to do first — a stick costs eight dollars and a
// moment, somebody's second SSD costs an afternoon and possibly the only copy
// of something.
func (d Device) Routine() bool { return d.Bus == "usb" && !d.System }

// Flashable is the old name for Routine, kept so that every caller which has
// not been reviewed against the wider policy keeps the narrow behaviour it was
// written for. New code should say which of the two it means.
//
// Deprecated: use Writable for "may I write here" or Routine for "is this the
// ordinary case".
func (d Device) Flashable() bool { return d.Routine() }

// String renders one listing row.
func (d Device) String() string {
	size := float64(d.SizeBytes) / (1 << 30)
	note := ""
	if d.System {
		note = "  [SYSTEM DISK — never writable]"
	} else if !d.Routine() {
		note = "  [fixed disk — writable, but confirm carefully]"
	}
	return fmt.Sprintf("%-22s %7.1f GiB  %-4s  %s%s", d.ID, size, d.Bus, d.Model, note)
}

// List enumerates disks, most-plausible flash targets first.
func List(ctx context.Context) ([]Device, error) {
	return list(ctx)
}

// SizeConfirmation is the typed string a caller must supply to arm a flash
// — the exact size in GiB with one decimal, e.g. "14.9": "type the SIZE
// exactly as shown".
func (d Device) SizeConfirmation() string {
	return fmt.Sprintf("%.1f", float64(d.SizeBytes)/(1<<30))
}
