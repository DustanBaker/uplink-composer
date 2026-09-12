package diskutil

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/DustanBaker/uplink-composer/internal/device"
)

// lsblkDisk mirrors the shape `lsblk --json` returns.
type lsblkDisk struct {
	Name     string      `json:"name"`
	Size     int64       `json:"size"`
	PTType   string      `json:"pttype"`
	Children []lsblkPart `json:"children"`
}

type lsblkPart struct {
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	FSType       string `json:"fstype"`
	Label        string `json:"label"`
	MountPoint   string `json:"mountpoint"`
	PartTypeName string `json:"parttypename"`
	Start        int64  `json:"start"`
}

// inspect uses lsblk, which reports the partition table without root.
func inspect(ctx context.Context, dev device.Device) (*Layout, error) {
	l := &Layout{Device: dev, Scheme: "unknown"}
	out, err := exec.CommandContext(ctx, "lsblk", "--json", "--bytes",
		"-o", "NAME,SIZE,PTTYPE,FSTYPE,LABEL,MOUNTPOINT,PARTTYPENAME,START", dev.ID).Output()
	if err != nil {
		return nil, fmt.Errorf("lsblk: %w", err)
	}
	var doc struct {
		BlockDevices []lsblkDisk `json:"blockdevices"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("parsing lsblk output: %w", err)
	}
	if len(doc.BlockDevices) == 0 {
		return nil, fmt.Errorf("lsblk reported nothing for %s", dev.ID)
	}
	d := doc.BlockDevices[0]
	switch strings.ToLower(d.PTType) {
	case "gpt":
		l.Scheme = "gpt"
	case "dos":
		l.Scheme = "mbr"
	case "":
		l.Scheme = "none"
	default:
		l.Scheme = d.PTType
	}
	for i, c := range d.Children {
		p := Partition{
			Number: i + 1,
			Offset: c.Start * 512,
			Size:   c.Size,
			Type:   firstNonEmpty(c.PartTypeName, c.FSType),
			Label:  c.Label,
		}
		if c.MountPoint != "" {
			p.Mounts = []string{c.MountPoint}
		}
		l.Usable += p.Size
		l.Parts = append(l.Parts, p)
	}
	return l, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// prepare writes a fresh table with sfdisk and formats with the matching
// mkfs. Both are shelled out to rather than reimplemented: these are the
// tools the distribution already trusts with its own disks.
func prepare(ctx context.Context, dev device.Device, opts Options, progress func(string)) error {
	mkfs, args := linuxMkfs(opts)
	if _, err := exec.LookPath(mkfs); err != nil {
		return fmt.Errorf("%s is not installed — it creates the %s filesystem (try your package manager)", mkfs, opts.FS)
	}
	if _, err := exec.LookPath("sfdisk"); err != nil {
		return fmt.Errorf("sfdisk is not installed — it writes the partition table (package util-linux)")
	}

	// One partition spanning the disk, in sfdisk's script format.
	label := "gpt"
	if opts.Scheme == MBR {
		label = "dos"
	}
	script := fmt.Sprintf("label: %s\n,,\n", label)
	progress(fmt.Sprintf("writing a %s table on %s", label, dev.ID))
	cmd := exec.CommandContext(ctx, "sfdisk", "--wipe", "always", dev.ID)
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sfdisk: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	// Let the kernel pick up the new table before formatting into it.
	exec.CommandContext(ctx, "partprobe", dev.ID).Run()
	exec.CommandContext(ctx, "udevadm", "settle").Run()

	part := partitionPath(dev.ID, 1)
	progress(fmt.Sprintf("formatting %s as %s", part, opts.FS))
	if out, err := exec.CommandContext(ctx, mkfs, append(args, part)...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %v\n%s", mkfs, err, strings.TrimSpace(string(out)))
	}
	progress("prepared " + dev.ID)
	return nil
}

func linuxMkfs(o Options) (string, []string) {
	switch o.FS {
	case NTFS:
		return "mkfs.ntfs", []string{"--quick", "--label", o.Label}
	case FAT32:
		return "mkfs.vfat", []string{"-F", "32", "-n", o.Label}
	default:
		return "mkfs.exfat", []string{"-n", o.Label}
	}
}

// partitionPath handles the two naming conventions: /dev/sdb1 but
// /dev/nvme0n1p1 and /dev/mmcblk0p1.
func partitionPath(disk string, n int) string {
	last := disk[len(disk)-1]
	if last >= '0' && last <= '9' {
		return fmt.Sprintf("%sp%d", disk, n)
	}
	return fmt.Sprintf("%s%d", disk, n)
}
