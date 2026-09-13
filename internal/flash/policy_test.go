package flash

import (
	"errors"
	"testing"

	"github.com/uplinkresearch/bootwright/internal/device"
)

// TestCheckTarget is the write policy, and the property that matters is the
// asymmetry: the system disk is refused with any permission, a fixed disk is
// refused without it, and a stick is always fine.
func TestCheckTarget(t *testing.T) {
	cases := []struct {
		name              string
		dev               device.Device
		plain, withPermit bool
	}{
		{"removable usb", device.Device{ID: "d", Bus: "usb"}, true, true},
		{"internal nvme", device.Device{ID: "d", Bus: "nvme"}, false, true},
		{"internal sata", device.Device{ID: "d", Bus: "scsi"}, false, true},

		// The one disk no answer unlocks.
		{"system disk", device.Device{ID: "d", Bus: "nvme", System: true}, false, false},
		{"system on usb", device.Device{ID: "d", Bus: "usb", System: true}, false, false},
	}
	for _, c := range cases {
		if err := CheckTarget(c.dev, false); (err == nil) != c.plain {
			t.Errorf("%s: without permission = %v, wanted allowed=%v", c.name, err, c.plain)
		}
		if err := CheckTarget(c.dev, true); (err == nil) != c.withPermit {
			t.Errorf("%s: with permission = %v, wanted allowed=%v", c.name, err, c.withPermit)
		}
	}
}

// TestFixedDiskIsDistinguishable: a caller that wants to offer "yes, I really
// mean that disk" has to be able to tell this refusal apart from a refusal
// nothing can lift. Matching on message text would be the wrong way.
func TestFixedDiskIsDistinguishable(t *testing.T) {
	fixed := CheckTarget(device.Device{ID: "d", Bus: "nvme"}, false)
	if !errors.Is(fixed, ErrFixedDisk) {
		t.Errorf("fixed-disk refusal does not wrap ErrFixedDisk: %v", fixed)
	}
	system := CheckTarget(device.Device{ID: "d", Bus: "nvme", System: true}, false)
	if errors.Is(system, ErrFixedDisk) {
		t.Error("the system disk refusal looks like a fixed-disk refusal — a UI would offer to override it")
	}
}
