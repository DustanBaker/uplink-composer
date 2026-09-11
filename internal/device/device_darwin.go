package device

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"howett.net/plist"
)

// diskutil list -plist / info -plist shapes (only the fields used).
type duList struct {
	WholeDisks []string `plist:"WholeDisks"`
}

type duInfo struct {
	DeviceIdentifier string `plist:"DeviceIdentifier"`
	MediaName        string `plist:"MediaName"`
	IORegistryEntryName string `plist:"IORegistryEntryName"`
	Size             int64  `plist:"Size"`
	BusProtocol      string `plist:"BusProtocol"`
	RemovableMedia   bool   `plist:"RemovableMedia"`
	Internal         bool   `plist:"Internal"`
	MountPoint       string `plist:"MountPoint"`
	OSInternalMedia  bool   `plist:"OSInternalMedia"`
}

func list(ctx context.Context) ([]Device, error) {
	out, err := exec.CommandContext(ctx, "diskutil", "list", "-plist", "physical").Output()
	if err != nil {
		return nil, fmt.Errorf("device: diskutil list: %w", err)
	}
	var l duList
	if _, err := plist.Unmarshal(out, &l); err != nil {
		return nil, fmt.Errorf("device: parsing diskutil plist: %w", err)
	}
	var devs []Device
	for _, id := range l.WholeDisks {
		ib, err := exec.CommandContext(ctx, "diskutil", "info", "-plist", id).Output()
		if err != nil {
			continue
		}
		var info duInfo
		if _, err := plist.Unmarshal(ib, &info); err != nil {
			continue
		}
		bus := strings.ToLower(info.BusProtocol)
		model := info.MediaName
		if model == "" {
			model = info.IORegistryEntryName
		}
		dev := Device{
			// rdisk: the raw node, ~10x faster than the buffered one.
			ID:        "/dev/r" + id,
			Index:     -1,
			Model:     strings.TrimSpace(model),
			SizeBytes: info.Size,
			Bus:       bus,
			Removable: info.RemovableMedia,
			System:    info.Internal || info.OSInternalMedia,
		}
		if info.MountPoint != "" {
			dev.Mounts = append(dev.Mounts, info.MountPoint)
		}
		devs = append(devs, dev)
	}
	return devs, nil
}
