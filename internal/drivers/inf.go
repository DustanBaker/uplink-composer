// Package drivers inspects Windows driver packages: what an INF covers
// (device class, provider, version, hardware IDs) so packs can be chosen
// and staged with confidence.
package drivers

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
)

// INF summarizes one driver INF file.
type INF struct {
	File        string // path relative to the scanned root
	Class       string
	Provider    string
	DriverDate  string
	DriverVer   string
	Devices     []string // resolved device descriptions (sample)
	HardwareIDs []string // deduped, uppercased
}

// hwidRe matches hardware-ID tokens (PCI\VEN_8086&DEV_..., USB\VID_...,
// HDAUDIO\..., ACPI\..., and friends).
var hwidRe = regexp.MustCompile(`(?i)\b(PCI|USB|USBSTOR|HDAUDIO|INTELAUDIO|ACPI|HID|BTHENUM|BTH|SD|SCSI|SWC|MSHW)\\[A-Z0-9_&.\-\\{}%]+`)

// ParseINF reads one INF (ANSI or UTF-16) and summarizes it.
func ParseINF(path string) (*INF, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := decodeINF(raw)
	sections := splitSections(text)

	strTab := map[string]string{}
	for name, lines := range sections {
		if !strings.HasPrefix(name, "strings") {
			continue // includes localized [Strings.0409] variants
		}
		for _, l := range lines {
			if k, v, ok := strings.Cut(l, "="); ok {
				strTab[strings.ToLower(strings.TrimSpace(k))] = strings.Trim(strings.TrimSpace(v), `"`)
			}
		}
	}
	resolve := func(s string) string {
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "%") && strings.HasSuffix(s, "%") && len(s) > 2 {
			if v, ok := strTab[strings.ToLower(s[1:len(s)-1])]; ok {
				return v
			}
		}
		return strings.Trim(s, `"`)
	}

	out := &INF{File: filepath.Base(path)}
	for _, l := range sections["version"] {
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		key, val := strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch key {
		case "class":
			out.Class = resolve(val)
		case "provider":
			out.Provider = resolve(val)
		case "driverver":
			date, ver, _ := strings.Cut(val, ",")
			out.DriverDate = strings.TrimSpace(date)
			out.DriverVer = strings.TrimSpace(ver)
		}
	}

	// Model sections named by [Manufacturer]: `%name% = section, os, os...`
	// map to [section], [section.NTamd64], … Each model line is
	// `%desc% = installSec, hwid[, compat-hwid...]`.
	modelBases := map[string]bool{}
	for _, l := range sections["manufacturer"] {
		if _, v, ok := strings.Cut(l, "="); ok {
			parts := strings.Split(v, ",")
			if len(parts) > 0 {
				modelBases[strings.ToLower(strings.TrimSpace(parts[0]))] = true
			}
		}
	}
	seenID := map[string]bool{}
	seenDev := map[string]bool{}
	for name, lines := range sections {
		base, _, _ := strings.Cut(name, ".")
		if !modelBases[base] {
			continue
		}
		for _, l := range lines {
			desc, rhs, ok := strings.Cut(l, "=")
			if !ok {
				continue
			}
			if d := resolve(desc); d != "" && !seenDev[d] {
				seenDev[d] = true
				out.Devices = append(out.Devices, d)
			}
			for _, id := range hwidRe.FindAllString(rhs, -1) {
				id = strings.ToUpper(id)
				if !seenID[id] {
					seenID[id] = true
					out.HardwareIDs = append(out.HardwareIDs, id)
				}
			}
		}
	}
	// Fallback for INFs whose structure defeats the parser: sweep the
	// whole file for hardware IDs.
	if len(out.HardwareIDs) == 0 {
		for _, id := range hwidRe.FindAllString(text, -1) {
			id = strings.ToUpper(id)
			if !seenID[id] {
				seenID[id] = true
				out.HardwareIDs = append(out.HardwareIDs, id)
			}
		}
	}
	sort.Strings(out.HardwareIDs)
	return out, nil
}

// ScanDir parses every *.inf under root (recursively).
func ScanDir(root string) ([]*INF, error) {
	var out []*INF
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".inf") {
			return err
		}
		inf, perr := ParseINF(p)
		if perr != nil {
			return fmt.Errorf("%s: %w", p, perr)
		}
		if rel, rerr := filepath.Rel(root, p); rerr == nil {
			inf.File = filepath.ToSlash(rel)
		}
		out = append(out, inf)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out, err
}

// decodeINF handles the UTF-16LE (BOM) encoding many vendor INFs use.
func decodeINF(raw []byte) string {
	if len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xFE {
		u := make([]uint16, 0, (len(raw)-2)/2)
		for i := 2; i+1 < len(raw); i += 2 {
			u = append(u, binary.LittleEndian.Uint16(raw[i:i+2]))
		}
		return string(utf16.Decode(u))
	}
	return string(raw)
}

// splitSections lowercases section names and strips comments/blank lines.
func splitSections(text string) map[string][]string {
	out := map[string][]string{}
	current := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if i := strings.Index(line, ";"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.Contains(line, "]") {
			current = strings.ToLower(line[1:strings.Index(line, "]")])
			continue
		}
		if current != "" {
			out[current] = append(out[current], line)
		}
	}
	return out
}
