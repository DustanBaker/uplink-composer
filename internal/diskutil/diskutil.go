// Package diskutil inspects and re-prepares removable disks: the "why is my
// 64 GB stick showing 3 GB" problem.
//
// That problem is largely one this tool causes. Writing a hybrid installer
// ISO leaves a partition layout Windows will not mount, or mounts as a small
// read-only volume, and the stick looks broken until someone knows to run
// diskpart. Owning the fix is fair.
//
// Two operations, deliberately separate:
//
//   - Inspect reads geometry and the partition table. It never needs
//     elevation, because making someone approve a prompt just to look at a
//     disk teaches them to approve prompts.
//   - Prepare rewrites the partition table and makes one full-size,
//     formatted volume. It is destructive, so it runs through the same
//     elevated worker and the same typed-size interlock as flashing.
//
// Scope is removable USB media only. The policy gate is device.Flashable(),
// the same one the flash engine uses — a partition editor that can reach an
// internal disk is a different tool with a different blast radius, and this
// is not it.
package diskutil

import (
	"context"
	"fmt"
	"strings"

	"github.com/DustanBaker/uplink-composer/internal/device"
)

// Scheme is the partition table to write.
type Scheme string

const (
	GPT Scheme = "gpt"
	MBR Scheme = "mbr"
)

// FS is the filesystem to create in the single partition.
type FS string

const (
	ExFAT FS = "exfat" // default: no 4 GiB file limit, read by everything current
	FAT32 FS = "fat32" // maximum compatibility, 4 GiB per-file ceiling
	NTFS  FS = "ntfs"  // Windows-only in practice
)

// Partition is one entry found on a disk.
type Partition struct {
	Number int
	Offset int64
	Size   int64
	Type   string   // scheme-specific type name or GUID
	Label  string   // volume label, when the OS could read one
	Mounts []string // drive letters or mount points
}

// Layout is what Inspect found.
type Layout struct {
	Device device.Device
	Scheme string // "gpt" | "mbr" | "none" | "unknown"
	Parts  []Partition
	// Usable is how much of the disk the partitions actually cover. The gap
	// between this and the device size is the whole point of the report: it
	// is what someone is looking at when a 64 GB stick reads as 3 GB.
	Usable int64
	// Notes explain, in plain words, anything that would make the OS behave
	// oddly with this disk.
	Notes []string
}

// UnusedBytes is capacity no partition claims.
func (l Layout) UnusedBytes() int64 {
	if l.Usable > l.Device.SizeBytes {
		return 0
	}
	return l.Device.SizeBytes - l.Usable
}

// Options configure Prepare.
type Options struct {
	Scheme Scheme
	FS     FS
	Label  string
}

func (o *Options) defaults() {
	if o.Scheme == "" {
		o.Scheme = GPT
	}
	if o.FS == "" {
		o.FS = ExFAT
	}
	if o.Label == "" {
		o.Label = "UPLINK"
	}
}

// Validate rejects options the platform tooling would refuse anyway, with a
// better explanation than it would give.
func (o Options) Validate(dev device.Device) error {
	switch o.Scheme {
	case GPT, MBR:
	default:
		return fmt.Errorf("scheme must be gpt or mbr, got %q", o.Scheme)
	}
	switch o.FS {
	case ExFAT, NTFS:
	case FAT32:
		// Windows' own formatter refuses FAT32 over 32 GB. The limit is in
		// the tool, not the filesystem, but we cannot format past it either.
		if dev.SizeBytes > 32<<30 {
			return fmt.Errorf("FAT32 cannot be created on a %.0f GB disk by Windows' formatter (32 GB limit) — use exFAT",
				float64(dev.SizeBytes)/1e9)
		}
	default:
		return fmt.Errorf("filesystem must be exfat, fat32, or ntfs, got %q", o.FS)
	}
	if len(o.Label) > 32 {
		return fmt.Errorf("label is %d characters; keep it to 32 or fewer", len(o.Label))
	}
	for _, r := range o.Label {
		if strings.ContainsRune(`\/:*?"<>|`, r) {
			return fmt.Errorf("label cannot contain %q", r)
		}
	}
	return nil
}

// Guard is the policy gate shared by every destructive operation here.
func Guard(dev device.Device) error {
	if dev.System {
		return fmt.Errorf("refusing %s: it hosts the running OS", dev.ID)
	}
	if !dev.Flashable() {
		return fmt.Errorf("refusing %s: disk utilities are limited to removable USB media (bus=%s) — "+
			"use the operating system's own disk manager for internal drives", dev.ID, dev.Bus)
	}
	return nil
}

// Inspect reads the disk's partition table without elevation.
func Inspect(ctx context.Context, dev device.Device) (*Layout, error) {
	l, err := inspect(ctx, dev)
	if err != nil {
		return nil, err
	}
	l.Notes = append(l.Notes, diagnose(*l)...)
	return l, nil
}

// diagnose turns a layout into the sentences someone actually needs: why a
// stick looks the wrong size, or why the OS will not mount it.
func diagnose(l Layout) []string {
	var notes []string
	switch {
	case len(l.Parts) == 0:
		notes = append(notes, "No partition table — most systems will not mount this until it is prepared.")
	case l.UnusedBytes() > l.Device.SizeBytes/10:
		notes = append(notes, fmt.Sprintf(
			"%.1f GB of %.1f GB is outside any partition. This is what a stick looks like after an installer image was written to it.",
			float64(l.UnusedBytes())/1e9, float64(l.Device.SizeBytes)/1e9))
	}
	for _, p := range l.Parts {
		t := strings.ToLower(p.Type)
		if strings.Contains(t, "iso9660") || strings.Contains(t, "0x00") {
			notes = append(notes, fmt.Sprintf("Partition %d looks like installer media, not a normal volume.", p.Number))
		}
	}
	if len(l.Parts) > 0 && len(mountsOf(l)) == 0 {
		notes = append(notes, "No volume is mounted — the filesystem is one this system cannot read, or the table is damaged.")
	}
	if len(notes) > 0 {
		notes = append(notes, "Prepare wipes the disk and gives it one full-size volume this system can use.")
	}
	return notes
}

func mountsOf(l Layout) []string {
	var out []string
	for _, p := range l.Parts {
		out = append(out, p.Mounts...)
	}
	return out
}

// Prepare wipes the disk and lays down one full-size formatted volume. It
// must run elevated; callers reach it through the flash worker.
func Prepare(ctx context.Context, dev device.Device, opts Options, progress func(stage string)) error {
	opts.defaults()
	if err := Guard(dev); err != nil {
		return err
	}
	if err := opts.Validate(dev); err != nil {
		return err
	}
	if progress == nil {
		progress = func(string) {}
	}
	return prepare(ctx, dev, opts, progress)
}
