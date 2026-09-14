package diskutil

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/uplinkresearch/dsky/internal/device"
	"github.com/yusufpapurcu/wmi"

	"github.com/uplinkresearch/dsky/internal/hidewin"
)

type win32Partition struct {
	DeviceID       string
	DiskIndex      uint32
	Index          uint32
	StartingOffset uint64
	Size           uint64
	Type           string
	Bootable       bool
}

type win32LogicalDisk struct {
	DeviceID   string
	VolumeName string
	FileSystem string
}

// inspect reads the partition table through WMI, which needs no elevation —
// the point being that looking at a disk should never cost a UAC prompt.
func inspect(_ context.Context, dev device.Device) (*Layout, error) {
	l := &Layout{Device: dev, Scheme: "unknown"}
	if dev.Index < 0 {
		return nil, fmt.Errorf("no Windows disk number for %s", dev.ID)
	}

	var parts []win32Partition
	q := fmt.Sprintf("SELECT DeviceID, DiskIndex, Index, StartingOffset, Size, Type, Bootable "+
		"FROM Win32_DiskPartition WHERE DiskIndex = %d", dev.Index)
	if err := wmi.Query(q, &parts); err != nil {
		return nil, fmt.Errorf("reading partitions: %w", err)
	}
	for _, p := range parts {
		part := Partition{
			Number: int(p.Index) + 1,
			Offset: int64(p.StartingOffset),
			Size:   int64(p.Size),
			Type:   p.Type,
		}
		part.Mounts, part.Label = volumesFor(p.DeviceID)
		l.Usable += part.Size
		l.Parts = append(l.Parts, part)
		// Win32_DiskPartition's Type string carries the scheme.
		if strings.Contains(strings.ToUpper(p.Type), "GPT") {
			l.Scheme = "gpt"
		} else if l.Scheme == "unknown" {
			l.Scheme = "mbr"
		}
	}
	if len(l.Parts) == 0 {
		l.Scheme = "none"
	}
	return l, nil
}

// volumesFor maps a partition to its drive letters and label. WMI models
// this as an association, so it needs its own query per partition.
func volumesFor(partitionID string) ([]string, string) {
	var disks []win32LogicalDisk
	q := fmt.Sprintf(
		"ASSOCIATORS OF {Win32_DiskPartition.DeviceID='%s'} WHERE AssocClass = Win32_LogicalDiskToPartition",
		strings.ReplaceAll(partitionID, `'`, `\'`))
	if err := wmi.Query(q, &disks); err != nil {
		return nil, ""
	}
	var mounts []string
	label := ""
	for _, d := range disks {
		mounts = append(mounts, d.DeviceID)
		if label == "" {
			label = d.VolumeName
		}
	}
	return mounts, label
}

// prepare drives diskpart. Its script language is unlovely, but it is built
// into every Windows including Server Core, it is what the OS itself uses,
// and it handles the volume-arrival timing that makes a hand-rolled
// "partition then format" race.
func prepare(ctx context.Context, dev device.Device, opts Options, progress func(string)) error {
	fsName := map[FS]string{ExFAT: "exfat", FAT32: "fat32", NTFS: "ntfs"}[opts.FS]
	scheme := "gpt"
	if opts.Scheme == MBR {
		scheme = "mbr"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "select disk %d\r\n", dev.Index)
	// Refuse to proceed if diskpart disagrees with us about what this disk
	// is. `clean` on the wrong disk is unrecoverable.
	fmt.Fprintf(&b, "detail disk\r\n")
	fmt.Fprintf(&b, "clean\r\n")
	fmt.Fprintf(&b, "convert %s\r\n", scheme)
	fmt.Fprintf(&b, "create partition primary\r\n")
	fmt.Fprintf(&b, "format fs=%s quick label=\"%s\"\r\n", fsName, opts.Label)
	fmt.Fprintf(&b, "assign\r\n")
	fmt.Fprintf(&b, "exit\r\n")

	script, err := os.CreateTemp("", "dsky-diskpart-*.txt")
	if err != nil {
		return err
	}
	path := script.Name()
	defer os.Remove(path)
	if _, err := script.WriteString(b.String()); err != nil {
		script.Close()
		return err
	}
	if err := script.Close(); err != nil {
		return err
	}

	progress(fmt.Sprintf("preparing %s as %s/%s", dev.ID, scheme, fsName))
	out, err := hidewin.Cmd(exec.CommandContext(ctx, "diskpart", "/s", path)).CombinedOutput()
	text := string(out)
	if err != nil {
		return fmt.Errorf("diskpart: %v\n%s", err, strings.TrimSpace(text))
	}
	// diskpart reports failures in its output and still exits 0, so the exit
	// code alone is not evidence that anything happened.
	low := strings.ToLower(text)
	for _, bad := range []string{"access is denied", "no disk selected", "the format did not complete",
		"diskpart has encountered an error", "is not valid"} {
		if strings.Contains(low, bad) {
			return fmt.Errorf("diskpart reported a problem:\n%s", strings.TrimSpace(text))
		}
	}
	progress("prepared " + dev.ID)
	return nil
}
