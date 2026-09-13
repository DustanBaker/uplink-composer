package diskutil

import (
	"strings"
	"testing"

	"github.com/uplinkresearch/dsky/internal/device"
)

func usb(size int64) device.Device {
	return device.Device{ID: `\\.\PhysicalDrive9`, Bus: "usb", SizeBytes: size}
}

// TestGuardPolicy is the safety property, and it has two halves.
//
// The system disk is refused no matter what anyone passes: there is no
// confirmation that makes overwriting the running OS survivable, so it is not
// offered as a choice. Every other disk is the owner's to erase — but a fixed
// disk only when permission was carried in explicitly, so that reaching one
// always traces back to somewhere a human was asked.
func TestGuardPolicy(t *testing.T) {
	cases := []struct {
		name              string
		dev               device.Device
		plain, withPermit bool // allowed with allowFixed=false / =true
	}{
		{"removable usb", device.Device{ID: "d", Bus: "usb"}, true, true},
		{"internal nvme", device.Device{ID: "d", Bus: "nvme"}, false, true},
		{"internal sata", device.Device{ID: "d", Bus: "scsi"}, false, true},
		{"unknown bus", device.Device{ID: "d"}, false, true},

		// No permission reaches these.
		{"system disk on usb", device.Device{ID: "d", Bus: "usb", System: true}, false, false},
		{"system nvme", device.Device{ID: "d", Bus: "nvme", System: true}, false, false},
	}
	for _, c := range cases {
		if err := Guard(c.dev, false); (err == nil) != c.plain {
			t.Errorf("%s: Guard(allowFixed=false) = %v, wanted allowed=%v", c.name, err, c.plain)
		}
		if err := Guard(c.dev, true); (err == nil) != c.withPermit {
			t.Errorf("%s: Guard(allowFixed=true) = %v, wanted allowed=%v", c.name, err, c.withPermit)
		}
	}
}

// TestPermissionIsNotInheritedByDefault: the zero Options must not be able to
// erase a fixed disk. Anything that forgets to think about this gets the narrow
// behaviour, which is the only default worth having here.
func TestPermissionIsNotInheritedByDefault(t *testing.T) {
	fixed := device.Device{ID: `\\.\PhysicalDrive1`, Bus: "nvme", SizeBytes: 1 << 40}
	if err := Prepare(nil, fixed, Options{}, nil); err == nil {
		t.Fatal("the zero Options erased a fixed disk — permission must be explicit")
	}
}

// TestPrepareRefusesBadTargets checks the guard actually runs inside Prepare,
// not just when a caller remembers to ask.
func TestPrepareRefusesBadTargets(t *testing.T) {
	sys := device.Device{ID: `\\.\PhysicalDrive0`, Bus: "nvme", System: true}
	if err := Prepare(nil, sys, Options{}, nil); err == nil {
		t.Fatal("Prepare accepted the system disk")
	} else if !strings.Contains(err.Error(), "running OS") {
		t.Errorf("unhelpful refusal: %v", err)
	}
}

func TestValidate(t *testing.T) {
	small, big := usb(16<<30), usb(64<<30)
	cases := []struct {
		name string
		dev  device.Device
		opts Options
		ok   bool
	}{
		{"exfat default", big, Options{Scheme: GPT, FS: ExFAT, Label: "DSKY"}, true},
		{"fat32 under 32G", small, Options{Scheme: MBR, FS: FAT32, Label: "BOOT"}, true},
		// Windows' formatter refuses FAT32 past 32 GB; saying so beats
		// letting the format fail halfway with a worse message.
		{"fat32 over 32G", big, Options{Scheme: GPT, FS: FAT32, Label: "BIG"}, false},
		{"bad scheme", big, Options{Scheme: "apm", FS: ExFAT}, false},
		{"bad fs", big, Options{Scheme: GPT, FS: "btrfs"}, false},
		{"label too long", big, Options{Scheme: GPT, FS: ExFAT, Label: strings.Repeat("x", 33)}, false},
		{"label with a path separator", big, Options{Scheme: GPT, FS: ExFAT, Label: `a\b`}, false},
		{"label with a colon", big, Options{Scheme: GPT, FS: ExFAT, Label: "a:b"}, false},
	}
	for _, c := range cases {
		err := c.opts.Validate(c.dev)
		if c.ok && err != nil {
			t.Errorf("%s: rejected: %v", c.name, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%s: accepted something the platform would refuse", c.name)
		}
	}
}

func TestDefaults(t *testing.T) {
	var o Options
	o.defaults()
	if o.Scheme != GPT || o.FS != ExFAT || o.Label == "" {
		t.Errorf("defaults are %+v; want GPT/exFAT with a label", o)
	}
	if err := o.Validate(usb(64 << 30)); err != nil {
		t.Errorf("the defaults do not validate on a 64 GB stick: %v", err)
	}
}

// TestDiagnoseExplainsTheShrunkStick covers the case this feature exists for:
// a 64 GB stick reading as a few GB after an installer image was written to
// it. The report has to say so in words, not just print numbers.
func TestDiagnoseExplainsTheShrunkStick(t *testing.T) {
	l := Layout{
		Device: usb(62_900_000_000),
		Scheme: "gpt",
		Parts: []Partition{
			{Number: 1, Size: 4_100_000_000, Type: "GPT: Basic Data", Mounts: []string{"D:"}},
			{Number: 2, Size: 5_000_000, Type: "GPT: System"},
		},
		Usable: 4_105_000_000,
	}
	notes := strings.Join(diagnose(l), " ")
	if !strings.Contains(notes, "outside any partition") {
		t.Errorf("no explanation of the unpartitioned space: %q", notes)
	}
	if !strings.Contains(notes, "installer image") {
		t.Errorf("does not name the likely cause: %q", notes)
	}
	if !strings.Contains(notes, "Prepare") {
		t.Errorf("does not say what to do about it: %q", notes)
	}

	// A healthy stick should say nothing at all.
	healthy := Layout{
		Device: usb(62_900_000_000),
		Scheme: "gpt",
		Parts:  []Partition{{Number: 1, Size: 62_800_000_000, Mounts: []string{"E:"}}},
		Usable: 62_800_000_000,
	}
	if n := diagnose(healthy); len(n) != 0 {
		t.Errorf("a healthy disk produced notes: %v", n)
	}
}

func TestUnusedBytes(t *testing.T) {
	l := Layout{Device: usb(1000), Usable: 400}
	if got := l.UnusedBytes(); got != 600 {
		t.Errorf("UnusedBytes = %d, want 600", got)
	}
	// Partitions can overlap or report oddly; never report negative space.
	over := Layout{Device: usb(1000), Usable: 1200}
	if got := over.UnusedBytes(); got != 0 {
		t.Errorf("UnusedBytes with over-reported partitions = %d, want 0", got)
	}
}
