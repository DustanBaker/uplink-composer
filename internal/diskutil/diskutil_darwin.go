package diskutil

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/uplinkresearch/dsky/internal/device"
	"howett.net/plist"
)

// inspect asks diskutil for the disk's layout as a plist, which needs no
// elevation.
func inspect(ctx context.Context, dev device.Device) (*Layout, error) {
	l := &Layout{Device: dev, Scheme: "unknown"}
	id := strings.TrimPrefix(dev.ID, "/dev/r") // rdiskN -> diskN for diskutil
	if !strings.HasPrefix(id, "disk") {
		id = strings.TrimPrefix(dev.ID, "/dev/")
	}
	out, err := exec.CommandContext(ctx, "diskutil", "list", "-plist", id).Output()
	if err != nil {
		return nil, fmt.Errorf("diskutil list: %w", err)
	}
	var doc struct {
		AllDisksAndPartitions []struct {
			Content    string `plist:"Content"`
			Partitions []struct {
				DeviceIdentifier string `plist:"DeviceIdentifier"`
				Content          string `plist:"Content"`
				VolumeName       string `plist:"VolumeName"`
				Size             int64  `plist:"Size"`
				MountPoint       string `plist:"MountPoint"`
			} `plist:"Partitions"`
		} `plist:"AllDisksAndPartitions"`
	}
	if _, err := plist.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("parsing diskutil output: %w", err)
	}
	if len(doc.AllDisksAndPartitions) == 0 {
		return nil, fmt.Errorf("diskutil reported nothing for %s", dev.ID)
	}
	d := doc.AllDisksAndPartitions[0]
	switch d.Content {
	case "GUID_partition_scheme":
		l.Scheme = "gpt"
	case "FDisk_partition_scheme":
		l.Scheme = "mbr"
	case "":
		l.Scheme = "none"
	default:
		l.Scheme = d.Content
	}
	for i, p := range d.Partitions {
		part := Partition{
			Number: i + 1,
			Size:   p.Size,
			Type:   p.Content,
			Label:  p.VolumeName,
		}
		if p.MountPoint != "" {
			part.Mounts = []string{p.MountPoint}
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(p.DeviceIdentifier, id+"s")); err == nil {
			part.Number = n
		}
		l.Usable += part.Size
		l.Parts = append(l.Parts, part)
	}
	return l, nil
}

// prepare uses diskutil eraseDisk, which writes the table and the filesystem
// in one step and handles unmounting on the way.
func prepare(ctx context.Context, dev device.Device, opts Options, progress func(string)) error {
	fsName := map[FS]string{ExFAT: "ExFAT", FAT32: "MS-DOS FAT32", NTFS: ""}[opts.FS]
	if fsName == "" {
		return fmt.Errorf("macOS cannot create NTFS volumes; use exFAT for a stick both systems read")
	}
	scheme := "GPT"
	if opts.Scheme == MBR {
		scheme = "MBR"
	}
	id := strings.TrimPrefix(dev.ID, "/dev/r")
	if !strings.HasPrefix(id, "disk") {
		id = strings.TrimPrefix(dev.ID, "/dev/")
	}
	progress(fmt.Sprintf("preparing %s as %s/%s", id, strings.ToLower(scheme), opts.FS))
	out, err := exec.CommandContext(ctx, "diskutil", "eraseDisk", fsName, opts.Label, scheme, id).CombinedOutput()
	if err != nil {
		return fmt.Errorf("diskutil eraseDisk: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	progress("prepared " + dev.ID)
	return nil
}
