package diskutil

import (
	"fmt"
	"sort"
	"strings"

	"github.com/uplinkresearch/bootwright/internal/device"
)

// Identity is what a person needs in order to recognise a disk before
// destroying it.
//
// The guardrail on writing to a fixed disk is not really the typing or the
// two-step arm — somebody determined to proceed will do both without reading.
// The guardrail is recognition: "1 TB Samsung, two partitions, one labelled
// Photos" is a sentence that stops the wrong person, and "\\.\PhysicalDrive2"
// is not.
type Identity struct {
	Device device.Device

	// Headline names the disk the way its owner would: model and size.
	Headline string
	// Kind is the shape of what is on it, in plain words.
	Kind string
	// Contents is one line per partition worth mentioning.
	Contents []string
	// Warnings are the reasons to stop, most serious first. A non-empty
	// Warnings list means the UI should be harder to click through.
	Warnings []string
	// Destructive says whether writing here would destroy data somebody is
	// likely to want. An empty stick is not destructive; a labelled NTFS
	// volume is.
	Destructive bool
	// Routine mirrors device.Routine: a removable stick, the ordinary case.
	Routine bool
}

// Identify describes a disk and what is on it.
//
// layout may be a zero Layout when inspection failed or was not attempted; the
// description degrades to what the device itself reports rather than refusing
// to say anything, because "we could not read the partition table" is itself
// worth showing beside a destructive button.
func Identify(dev device.Device, layout Layout, inspectErr error) Identity {
	id := Identity{
		Device:   dev,
		Headline: headline(dev),
		Routine:  dev.Routine(),
	}

	if dev.System {
		id.Warnings = append(id.Warnings,
			"This is the disk the operating system is running from. It can never be written to.")
		id.Destructive = true
	} else if !dev.Routine() {
		id.Warnings = append(id.Warnings,
			"This is a fixed internal disk, not a removable stick. Writing to it destroys whatever machine depends on it.")
	}

	if inspectErr != nil {
		id.Kind = "contents unknown"
		id.Warnings = append(id.Warnings,
			"Its partition table could not be read, so what is on it is unknown. That alone is a reason to be sure.")
		return id
	}

	id.Contents, id.Destructive = describeParts(layout)
	id.Kind = kindOf(dev, layout)

	// Mounted volumes are the strongest signal that a disk is in use right now.
	if m := allMounts(dev, layout); len(m) > 0 {
		id.Warnings = append(id.Warnings,
			fmt.Sprintf("It is mounted and in use right now as %s.", strings.Join(m, ", ")))
	}
	for _, n := range layout.Notes {
		// diagnose() ends its findings by recommending Prepare, which is right
		// in the disk list and wrong everywhere else: telling somebody how to
		// erase a disk, inside the confirmation for copying onto it, is an
		// instruction for a different job in the middle of this one.
		if strings.HasPrefix(n, "Prepare wipes the disk") {
			continue
		}
		id.Warnings = append(id.Warnings, n)
	}
	return id
}

func headline(d device.Device) string {
	model := strings.TrimSpace(d.Model)
	if model == "" {
		model = "Unknown disk"
	}
	h := fmt.Sprintf("%s — %s", model, HumanSize(d.SizeBytes))
	if s := strings.TrimSpace(d.Serial); s != "" {
		h += fmt.Sprintf(" · serial %s", s)
	}
	return h
}

// kindOf is the one-line answer to "what is this?".
func kindOf(d device.Device, l Layout) string {
	where := "fixed disk"
	if d.Bus == "usb" {
		where = "USB disk"
		if d.Removable {
			where = "removable USB stick"
		}
	} else if d.Bus != "" {
		where = d.Bus + " disk"
	}
	switch {
	case d.System:
		return "the running system disk (" + where + ")"
	case len(l.Parts) == 0:
		return "an empty " + where + ", no partitions"
	case len(l.Parts) == 1:
		return fmt.Sprintf("a %s with one partition", where)
	default:
		return fmt.Sprintf("a %s with %d partitions", where, len(l.Parts))
	}
}

// describeParts renders one line per partition and reports whether any of them
// look like something a person would miss.
func describeParts(l Layout) ([]string, bool) {
	var out []string
	destructive := false
	for _, p := range l.Parts {
		bits := []string{HumanSize(p.Size)}
		if t := strings.TrimSpace(p.Type); t != "" {
			bits = append(bits, t)
		}
		if lb := strings.TrimSpace(p.Label); lb != "" {
			bits = append(bits, fmt.Sprintf("labelled %q", lb))
			destructive = true
		}
		if len(p.Mounts) > 0 {
			bits = append(bits, "mounted at "+strings.Join(p.Mounts, ", "))
			destructive = true
		}
		out = append(out, fmt.Sprintf("Partition %d: %s", p.Number, strings.Join(bits, ", ")))
	}
	if free := l.UnusedBytes(); free > 0 && len(l.Parts) > 0 {
		out = append(out, fmt.Sprintf("%s is outside any partition", HumanSize(free)))
	}
	return out, destructive
}

// allMounts merges the mounts the device reports with the ones found per
// partition, deduplicated — either source alone can be empty depending on
// platform.
func allMounts(d device.Device, l Layout) []string {
	seen := map[string]bool{}
	for _, m := range d.Mounts {
		if m = strings.TrimSpace(m); m != "" {
			seen[m] = true
		}
	}
	for _, p := range l.Parts {
		for _, m := range p.Mounts {
			if m = strings.TrimSpace(m); m != "" {
				seen[m] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// HumanSize renders a byte count the way a disk's owner thinks of it.
func HumanSize(b int64) string {
	switch {
	case b <= 0:
		return "unknown size"
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(b)/(1<<20))
	default:
		return fmt.Sprintf("%d bytes", b)
	}
}
