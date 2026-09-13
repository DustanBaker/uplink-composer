package webui

import (
	"strings"
	"testing"

	"github.com/uplinkresearch/bootwright/internal/device"
	"github.com/uplinkresearch/bootwright/internal/diskutil"
)

func dev(id string, size int64, bus string) device.Device {
	return device.Device{ID: id, Bus: bus, SizeBytes: size, Model: "Test"}
}

// TestConfirmationCoversEveryDestination: one box standing in for several disks
// would be one answer to several different questions. Adding a stick must
// change what has to be typed, or the second stick was never confirmed.
func TestConfirmationCoversEveryDestination(t *testing.T) {
	a := dev(`\\.\PhysicalDrive2`, 64<<30, "usb")
	b := dev(`\\.\PhysicalDrive3`, 32<<30, "usb")

	one := confirmationFor([]device.Device{a})
	two := confirmationFor([]device.Device{a, b})
	if one == two {
		t.Fatal("adding a destination did not change the confirmation")
	}
	if !strings.Contains(two, a.SizeConfirmation()) || !strings.Contains(two, b.SizeConfirmation()) {
		t.Errorf("confirmation %q does not name both disks", two)
	}
}

// TestShortNamesTheDisk: a progress line reading the full device path is
// unreadable with twenty of them stacked up.
func TestShortNamesTheDisk(t *testing.T) {
	for in, want := range map[string]string{
		`\\.\PhysicalDrive2`: "PhysicalDrive2",
		"/dev/sdb":           "sdb",
		"/dev/rdisk5":        "rdisk5",
		"plain":              "plain",
	} {
		if got := short(in); got != want {
			t.Errorf("short(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestIdentityCarriesTheConfirmation: the page shows what a disk is and what
// must be typed to erase it from the same call, so the two cannot disagree.
func TestIdentityCarriesTheConfirmation(t *testing.T) {
	d := dev(`\\.\PhysicalDrive1`, 1<<40, "nvme")
	out := identityJSON(diskutil.Identify(d, diskutil.Layout{Device: d}, nil))

	if out.Confirm != d.SizeConfirmation() {
		t.Errorf("Confirm = %q, want %q", out.Confirm, d.SizeConfirmation())
	}
	if out.Routine {
		t.Error("an nvme disk was reported as routine")
	}
	if !out.Writable {
		t.Error("a non-system fixed disk was reported as unwritable")
	}
	if len(out.Warnings) == 0 {
		t.Error("a fixed disk produced no warning for the page to show")
	}
}

// TestTheSystemDiskIsNotWritable, in the payload the page decides from.
func TestTheSystemDiskIsNotWritable(t *testing.T) {
	d := device.Device{ID: `\\.\PhysicalDrive0`, Bus: "nvme", System: true, SizeBytes: 1 << 40}
	out := identityJSON(diskutil.Identify(d, diskutil.Layout{Device: d}, nil))
	if out.Writable {
		t.Fatal("the running system disk was sent to the page as writable")
	}
}
