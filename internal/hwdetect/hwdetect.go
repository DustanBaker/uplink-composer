// Package hwdetect inspects the machine it runs on — system make/model, CPU,
// GPUs, NICs, and every PnP/PCI device's hardware ID and class — so Quick
// Install can resolve the exact drivers a machine of this model needs. The
// assumption is the common one: you build media on (or matching) the target
// hardware.
package hwdetect

import (
	"context"
	"strings"
)

// Hardware is a machine's detected profile.
type Hardware struct {
	Vendor  string   `json:"vendor"` // system manufacturer, e.g. "Dell Inc."
	Model   string   `json:"model"`  // product name, e.g. "OptiPlex 7010"
	CPU     string   `json:"cpu"`
	GPUs    []Device `json:"gpus"`
	NICs    []Device `json:"nics"`
	Devices []Device `json:"devices"` // everything, deduped
}

// Device is one piece of hardware.
type Device struct {
	Name       string `json:"name"`
	Class      string `json:"class,omitempty"`      // Display, Net, ...
	HardwareID string `json:"hardware_id"`          // PCI\VEN_xxxx&DEV_yyyy...
	GPUVendor  string `json:"gpu_vendor,omitempty"` // nvidia | amd | intel (GPUs only)
}

// Detect profiles the current machine.
func Detect(ctx context.Context) (*Hardware, error) { return detect(ctx) }

// KnownVendor maps a system-manufacturer string to a driver-catalog vendor
// (dell/lenovo/hp), or "" when there is no per-model feed for it.
func (h *Hardware) KnownVendor() string {
	v := strings.ToLower(h.Vendor)
	switch {
	case strings.Contains(v, "dell"):
		return "dell"
	case strings.Contains(v, "lenovo"):
		return "lenovo"
	case strings.Contains(v, "hp") || strings.Contains(v, "hewlett"):
		return "hp"
	case strings.Contains(v, "framework"):
		return "framework"
	}
	return ""
}

// VenDevID is the hardware ID trimmed to its PCI vendor+device pair
// (PCI\VEN_xxxx&DEV_yyyy) — the shape the Microsoft Update Catalog's search
// index matches. The full ID carries SUBSYS and REV, which finds nothing
// there. Empty for a device with no PCI id (USB, root-enumerated).
func (d Device) VenDevID() string {
	ven, dev := venDevFromHWID(d.HardwareID)
	if ven == "" || dev == "" {
		return ""
	}
	return "PCI\\VEN_" + ven + "&DEV_" + dev
}

// DriverHWIDs are the devices worth a per-device catalog lookup: GPUs and
// network adapters. Everything else either ships in-box with Windows or
// arrives from Windows Update, and each lookup is one scrape of a site that
// rate-limits — so this list stays deliberately short.
func (h *Hardware) DriverHWIDs() []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range append(append([]Device{}, h.GPUs...), h.NICs...) {
		id := d.VenDevID()
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// gpuVendor maps a PCI vendor id (hex, no 0x) to a GPU brand.
func gpuVendor(venID string) string {
	switch strings.ToUpper(venID) {
	case "10DE":
		return "nvidia"
	case "1002", "1022":
		return "amd"
	case "8086":
		return "intel"
	}
	return ""
}

// venDevFromHWID pulls the VEN/DEV hex out of a PCI hardware ID.
func venDevFromHWID(id string) (ven, dev string) {
	u := strings.ToUpper(id)
	if i := strings.Index(u, "VEN_"); i >= 0 && len(u) >= i+8 {
		ven = u[i+4 : i+8]
	}
	if i := strings.Index(u, "DEV_"); i >= 0 && len(u) >= i+8 {
		dev = u[i+4 : i+8]
	}
	return ven, dev
}
