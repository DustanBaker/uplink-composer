package diskutil

import (
	"errors"
	"strings"
	"testing"

	"github.com/uplinkresearch/bootwright/internal/device"
)

func joined(s []string) string { return strings.ToLower(strings.Join(s, " | ")) }

// TestNamesTheDiskTheWayItsOwnerWould: the headline is the guardrail. Somebody
// about to erase the wrong disk is stopped by recognising the model and size,
// not by a device path they have never seen before.
func TestNamesTheDiskTheWayItsOwnerWould(t *testing.T) {
	dev := device.Device{ID: `\\.\PhysicalDrive2`, Model: "Samsung SSD 990 PRO 1TB",
		Serial: "S6Z1NJ0T", SizeBytes: 1 << 40, Bus: "nvme"}
	id := Identify(dev, Layout{Device: dev}, nil)
	for _, want := range []string{"Samsung SSD 990 PRO 1TB", "1024.0 GB", "S6Z1NJ0T"} {
		if !strings.Contains(id.Headline, want) {
			t.Errorf("headline %q is missing %q", id.Headline, want)
		}
	}
}

// TestWarnsBeforeAFixedDisk: the whole reason fixed disks are allowed is that
// the warning does the work. If this goes quiet the permission is unguarded.
func TestWarnsBeforeAFixedDisk(t *testing.T) {
	dev := device.Device{ID: "d", Bus: "nvme", Model: "WD Blue", SizeBytes: 500 << 30}
	id := Identify(dev, Layout{Device: dev}, nil)
	if id.Routine {
		t.Error("an nvme disk was reported as a routine target")
	}
	if !strings.Contains(joined(id.Warnings), "fixed internal disk") {
		t.Errorf("no warning that this is a fixed disk: %v", id.Warnings)
	}
}

// TestTheSystemDiskSaysSo, in the words that explain why there is no override.
func TestTheSystemDiskSaysSo(t *testing.T) {
	dev := device.Device{ID: "d", Bus: "nvme", System: true, Model: "boot", SizeBytes: 1 << 40}
	id := Identify(dev, Layout{Device: dev}, nil)
	if !strings.Contains(joined(id.Warnings), "never be written") {
		t.Errorf("the system disk did not say it is off limits: %v", id.Warnings)
	}
	if !strings.Contains(id.Kind, "running system disk") {
		t.Errorf("Kind = %q, wanted it to name the system disk", id.Kind)
	}
}

// TestDescribesWhatIsOnIt: labels and mount points are what somebody recognises
// their own data by, and both mark the write as destroying something wanted.
func TestDescribesWhatIsOnIt(t *testing.T) {
	dev := device.Device{ID: "d", Bus: "usb", Model: "SanDisk", SizeBytes: 64 << 30}
	l := Layout{Device: dev, Scheme: "gpt", Usable: 64 << 30, Parts: []Partition{
		{Number: 1, Size: 64 << 30, Type: "ntfs", Label: "Photos", Mounts: []string{"E:"}},
	}}
	id := Identify(dev, l, nil)
	got := joined(id.Contents)
	for _, want := range []string{"photos", "e:", "ntfs"} {
		if !strings.Contains(got, want) {
			t.Errorf("contents %q missing %q", got, want)
		}
	}
	if !id.Destructive {
		t.Error("a labelled, mounted volume was not marked destructive")
	}
	if !strings.Contains(joined(id.Warnings), "in use right now") {
		t.Errorf("no warning that it is mounted: %v", id.Warnings)
	}
}

// TestAnEmptyStickIsNotDramatic: warning about a blank stick teaches people to
// dismiss warnings, which is how the real one gets dismissed too.
func TestAnEmptyStickIsNotDramatic(t *testing.T) {
	dev := device.Device{ID: "d", Bus: "usb", Removable: true, Model: "SanDisk", SizeBytes: 32 << 30}
	id := Identify(dev, Layout{Device: dev}, nil)
	if id.Destructive {
		t.Error("an empty stick was marked destructive")
	}
	if !id.Routine {
		t.Error("a removable usb stick was not treated as the routine case")
	}
	if len(id.Warnings) != 0 {
		t.Errorf("an empty stick produced warnings: %v", id.Warnings)
	}
}

// TestUnreadableIsItsOwnWarning: failing to read the table must not render as
// an empty, reassuring "nothing on it".
func TestUnreadableIsItsOwnWarning(t *testing.T) {
	dev := device.Device{ID: "d", Bus: "usb", SizeBytes: 32 << 30}
	id := Identify(dev, Layout{}, errors.New("access denied"))
	if !strings.Contains(joined(id.Warnings), "could not be read") {
		t.Errorf("an unreadable disk did not say so: %v", id.Warnings)
	}
	if id.Kind != "contents unknown" {
		t.Errorf("Kind = %q, wanted it to admit it does not know", id.Kind)
	}
}

// TestDoesNotAdvertisePrepareInsideAnotherJob: diagnose() ends with "Prepare
// wipes the disk…", which is right in the disk list and wrong in the
// confirmation for copying onto that disk — an instruction for a different job
// in the middle of this one.
func TestDoesNotAdvertisePrepareInsideAnotherJob(t *testing.T) {
	dev := device.Device{ID: "d", Bus: "usb", Model: "SanDisk", SizeBytes: 64 << 30}
	l := Layout{Device: dev, Usable: 4 << 30, Parts: []Partition{
		{Number: 1, Size: 4 << 30, Type: "iso9660"},
	}}
	l.Notes = diagnose(l)
	if len(l.Notes) == 0 {
		t.Fatal("the fixture produced no notes, so this proves nothing")
	}
	id := Identify(dev, l, nil)
	for _, w := range id.Warnings {
		if strings.HasPrefix(w, "Prepare wipes the disk") {
			t.Errorf("the identity card recommends Prepare: %q", w)
		}
	}
	// The findings themselves must survive; only the call to action goes.
	if !strings.Contains(joined(id.Warnings), "outside any partition") {
		t.Errorf("the useful diagnosis was dropped too: %v", id.Warnings)
	}
}
