package hwdetect

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

func detect(ctx context.Context) (*Hardware, error) {
	h := &Hardware{}
	h.Vendor = readDMI("sys_vendor")
	h.Model = readDMI("product_name")
	h.CPU = cpuModel()

	// lspci -Dvmmnn gives machine-readable class/vendor/device with hex ids.
	out, err := exec.CommandContext(ctx, "lspci", "-Dvmmnn").Output()
	if err != nil {
		return h, nil // no lspci: return what DMI gave us
	}
	for _, block := range strings.Split(string(out), "\n\n") {
		d, class := parseLspciBlock(block)
		if d.HardwareID == "" {
			continue
		}
		switch class {
		case "0300", "0302", "0380": // VGA / 3D / display controllers
			ven, _ := venDevFromHWID(d.HardwareID)
			d.Class = "Display"
			d.GPUVendor = gpuVendor(ven)
			h.GPUs = append(h.GPUs, d)
		case "0200", "0280": // ethernet / network controllers
			d.Class = "Net"
			h.NICs = append(h.NICs, d)
		}
		h.Devices = append(h.Devices, d)
	}
	return h, nil
}

func readDMI(name string) string {
	b, err := os.ReadFile("/sys/devices/virtual/dmi/id/" + name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func cpuModel() string {
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "model name" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

var hexID = regexp.MustCompile(`\[([0-9a-fA-F]{4})\]`)

// parseLspciBlock turns one lspci -Dvmmnn record into a Device + its class
// code, building a Windows-style PCI\VEN_&DEV_ id so the same driver-catalog
// lookups work cross-platform.
func parseLspciBlock(block string) (Device, string) {
	var d Device
	var classCode, ven, dev, sven, sdev, name string
	for _, line := range strings.Split(block, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "Class":
			if m := hexID.FindStringSubmatch(val); m != nil {
				classCode = m[1]
			}
		case "Vendor":
			if m := hexID.FindStringSubmatch(val); m != nil {
				ven = m[1]
			}
		case "Device":
			if m := hexID.FindStringSubmatch(val); m != nil {
				dev = m[1]
			}
			name = hexID.ReplaceAllString(val, "")
		case "SVendor":
			if m := hexID.FindStringSubmatch(val); m != nil {
				sven = m[1]
			}
		case "SDevice":
			if m := hexID.FindStringSubmatch(val); m != nil {
				sdev = m[1]
			}
		}
	}
	if ven == "" || dev == "" {
		return d, ""
	}
	id := "PCI\\VEN_" + strings.ToUpper(ven) + "&DEV_" + strings.ToUpper(dev)
	if sven != "" && sdev != "" {
		id += "&SUBSYS_" + strings.ToUpper(sdev+sven)
	}
	d.Name = strings.TrimSpace(name)
	d.HardwareID = id
	return d, classCode
}
