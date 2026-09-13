package device

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// lsblk -J output shapes.
type lsblkOut struct {
	Blockdevices []lsblkDev `json:"blockdevices"`
}

type lsblkDev struct {
	Name       string      `json:"name"`
	Model      *string     `json:"model"`
	Serial     *string     `json:"serial"`
	Size       json.Number `json:"size"`
	Tran       *string     `json:"tran"`
	RM         bool        `json:"rm"`
	Type       string      `json:"type"`
	Mountpoint *string     `json:"mountpoint"`
	Children   []lsblkDev  `json:"children"`
}

func list(ctx context.Context) ([]Device, error) {
	if isWSL() {
		return nil, fmt.Errorf("device: running under WSL, which cannot see USB block devices — use the Windows dsky.exe instead")
	}
	cmd := exec.CommandContext(ctx, "lsblk", "-J", "-b", "-o", "NAME,MODEL,SERIAL,SIZE,TRAN,RM,TYPE,MOUNTPOINT")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("device: lsblk: %w", err)
	}
	var parsed lsblkOut
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("device: parsing lsblk output: %w", err)
	}
	var devs []Device
	for _, d := range parsed.Blockdevices {
		if d.Type != "disk" {
			continue
		}
		size, _ := strconv.ParseInt(d.Size.String(), 10, 64)
		dev := Device{
			ID:        "/dev/" + d.Name,
			Index:     -1,
			Model:     strings.TrimSpace(deref(d.Model)),
			Serial:    strings.TrimSpace(deref(d.Serial)),
			SizeBytes: size,
			Bus:       strings.ToLower(deref(d.Tran)),
			Removable: d.RM,
		}
		collectMounts(d, &dev)
		devs = append(devs, dev)
	}
	return devs, nil
}

// collectMounts walks children marking mounts; "/", "/boot", "/home" (or
// swap) anywhere on the disk marks it as the system disk.
func collectMounts(d lsblkDev, dev *Device) {
	if mp := deref(d.Mountpoint); mp != "" {
		dev.Mounts = append(dev.Mounts, mp)
		if mp == "/" || mp == "/boot" || mp == "/boot/efi" || mp == "/home" || mp == "[SWAP]" {
			dev.System = true
		}
	}
	for _, c := range d.Children {
		collectMounts(c, dev)
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func isWSL() bool {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}
