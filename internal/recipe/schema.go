// Package recipe defines the YAML recipe schema — the unit of composition:
// one OS source + target layout + (for Windows) unattend, driver packs,
// payload, and first-boot behavior.
package recipe

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// OSType selects the composition pipeline.
type OSType string

const (
	OSWindows  OSType = "windows"  // extracted/staged FAT32 install media
	OSLinuxISO OSType = "linux-iso" // hybrid ISO, raw-written
	OSRawImg   OSType = "raw-img"  // appliance image, raw-written (may be compressed)
)

// SourceMode says how a Windows OS source is interpreted.
type SourceMode string

const (
	SourceAuto SourceMode = "auto" // iso for .iso blobs, tree for tree_path
	SourceISO  SourceMode = "iso"
	SourceTree SourceMode = "tree" // captured master directory (already split .swm)
)

// Recipe is one buildable media definition.
type Recipe struct {
	Version int    `yaml:"version"`
	ID      string `yaml:"id"`
	Name    string `yaml:"name,omitempty"`

	OS     OSSpec     `yaml:"os"`
	Target TargetSpec `yaml:"target"`

	Windows *WindowsSpec `yaml:"windows,omitempty"`

	Flash         FlashSpec         `yaml:"flash,omitempty"`
	FirmwareNotes string            `yaml:"firmware_notes,omitempty"`
	Vars          map[string]string `yaml:"vars,omitempty"`

	// Path the recipe was loaded from (not serialized).
	Path string `yaml:"-"`
}

type OSSpec struct {
	Source     string     `yaml:"source"` // manifest/library id (iso mode)
	Type       OSType     `yaml:"type"`
	SourceMode SourceMode `yaml:"source_mode,omitempty"`
	// TreePath points at a captured-master directory (tree mode), e.g. the
	// nuc-usb-master rsync target. Absolute, or relative to the workspace.
	TreePath string `yaml:"tree_path,omitempty"`
}

type TargetSpec struct {
	Scheme      string `yaml:"scheme,omitempty"`       // mbr (default) | gpt
	Filesystem  string `yaml:"filesystem,omitempty"`   // fat32 (only value in v1)
	VolumeLabel string `yaml:"volume_label,omitempty"` // default ESD-USB
	Size        string `yaml:"size,omitempty"`         // auto (default) | "8GiB"
	MinStick    string `yaml:"min_stick,omitempty"`    // informational check at flash time
	Boot        string `yaml:"boot,omitempty"`         // uefi-only (only value in v1)
}

type WindowsSpec struct {
	EICfg        *EICfg        `yaml:"ei_cfg,omitempty"`
	Unattend     *UnattendSpec `yaml:"unattend,omitempty"`
	WinPEDrivers []string      `yaml:"winpe_drivers,omitempty"` // source refs → $WinpeDriver$/
	DriverPacks  []DriverPack  `yaml:"driver_packs,omitempty"`
	Payload      []PayloadItem `yaml:"payload,omitempty"`
	Debloat      *DebloatSpec  `yaml:"debloat,omitempty"`
	Firstboot    FirstbootSpec `yaml:"firstboot,omitempty"`
}

// DebloatSpec strips consumer junk at first boot while keeping the media
// itself official (update- and activation-safe): provisioned-app removal
// plus ad/telemetry/Copilot/widgets policies. Preset "standard" removes
// promo and gaming apps; "aggressive" also drops Outlook (new), Phone Link,
// Maps, People, and Get Help.
type DebloatSpec struct {
	Preset     string   `yaml:"preset"`                // off | standard | aggressive
	RemoveApps []string `yaml:"remove_apps,omitempty"` // extra Appx family-name prefixes
	KeepApps   []string `yaml:"keep_apps,omitempty"`   // exceptions to the preset lists
}

// Enabled reports whether the spec actually does anything.
func (d *DebloatSpec) Enabled() bool {
	return d != nil && d.Preset != "" && d.Preset != "off"
}

// EICfg pins the edition Windows Setup installs (sources/ei.cfg).
type EICfg struct {
	Edition string `yaml:"edition"`           // e.g. Professional
	Channel string `yaml:"channel,omitempty"` // Retail (default) | OEM | Volume
	VL      bool   `yaml:"vl,omitempty"`
}

type UnattendSpec struct {
	Template string            `yaml:"template"` // workspace-relative
	Vars     map[string]string `yaml:"vars,omitempty"`
}

// InstallMethod is how a driver pack gets installed at first boot.
type InstallMethod string

const (
	InstallSweep         InstallMethod = "pnputil-sweep"     // INF dir under Scripts/Drivers/, swept by pnputil
	InstallExpandSweep   InstallMethod = "expand-then-sweep" // .cab expanded first, then swept
	InstallExe           InstallMethod = "exe"               // vendor silent installer
	InstallWinPEOnlyNote InstallMethod = ""                  // zero value: validated away
)

// DriverPack references driver material by library/manifest Ref or by a
// workspace-relative Path (a directory of INFs kept in the workspace repo,
// like nuc-deployment-usb's Drivers/). Exactly one is set.
type DriverPack struct {
	Ref     string        `yaml:"ref,omitempty"`
	Path    string        `yaml:"path,omitempty"`
	Install InstallMethod `yaml:"install"`
	Args    []string      `yaml:"args,omitempty"` // exe only
	Log     string        `yaml:"log,omitempty"`  // exe only; log filename
}

// Name is the pack's staging directory name.
func (d DriverPack) Name() string {
	if d.Ref != "" {
		return d.Ref
	}
	return filepath.Base(filepath.FromSlash(d.Path))
}

// PayloadItem stages one extra file into Scripts/. Exactly one of Ref
// (library/manifest id) or Path (workspace-relative file) is set.
type PayloadItem struct {
	Ref  string `yaml:"ref,omitempty"`
	Path string `yaml:"path,omitempty"`
}

type FirstbootSpec struct {
	// Mode "generate" (default) builds the script from Steps; "template"
	// renders Template verbatim for byte-exact control.
	Mode     string `yaml:"mode,omitempty"`
	Template string `yaml:"template,omitempty"`
	Steps    []Step `yaml:"steps,omitempty"`
	Log      string `yaml:"log,omitempty"` // default firstboot.log
}

// Step is one ordered first-boot action. YAML forms:
//
//	- drivers                      # expand cabs, pnputil sweep, run driver exes
//	- debloat                      # run the generated debloat pass (requires windows.debloat)
//	- wait: 10s
//	- msi: { ref: x, args: ["/qn"], log: x.log }
//	- exe: { ref: x, args: ["-s"], log: x.log }
//	- cmd: "raw command line"
type Step struct {
	Drivers bool
	Debloat bool
	Wait    time.Duration
	MSI     *RunItem
	Exe     *RunItem
	Cmd     string
}

type RunItem struct {
	Ref  string   `yaml:"ref"`
	Args []string `yaml:"args,omitempty"`
	Log  string   `yaml:"log,omitempty"`
}

func (s *Step) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		switch node.Value {
		case "drivers":
			s.Drivers = true
			return nil
		case "debloat":
			s.Debloat = true
			return nil
		}
		return fmt.Errorf("line %d: unknown firstboot step %q (scalar steps: drivers, debloat)", node.Line, node.Value)
	}
	if node.Kind != yaml.MappingNode || len(node.Content) != 2 {
		return fmt.Errorf("line %d: a firstboot step is either a scalar or a single-key map", node.Line)
	}
	key, val := node.Content[0].Value, node.Content[1]
	switch key {
	case "wait":
		d, err := time.ParseDuration(val.Value)
		if err != nil {
			return fmt.Errorf("line %d: wait: %v", node.Line, err)
		}
		s.Wait = d
	case "msi":
		s.MSI = &RunItem{}
		return val.Decode(s.MSI)
	case "exe":
		s.Exe = &RunItem{}
		return val.Decode(s.Exe)
	case "cmd":
		s.Cmd = val.Value
	default:
		return fmt.Errorf("line %d: unknown firstboot step %q", node.Line, key)
	}
	return nil
}

type FlashSpec struct {
	Verify string `yaml:"verify,omitempty"` // readback-sha256 (default) | none
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Load reads and validates a recipe file.
func Load(path string) (*Recipe, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Recipe
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	r.Path = path
	r.applyDefaults()
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return &r, nil
}

func (r *Recipe) applyDefaults() {
	if r.Target.Scheme == "" {
		r.Target.Scheme = "mbr"
	}
	if r.Target.Filesystem == "" {
		r.Target.Filesystem = "fat32"
	}
	if r.Target.VolumeLabel == "" {
		r.Target.VolumeLabel = "ESD-USB"
	}
	if r.Target.Size == "" {
		r.Target.Size = "auto"
	}
	if r.Target.Boot == "" {
		r.Target.Boot = "uefi-only"
	}
	if r.OS.SourceMode == "" {
		r.OS.SourceMode = SourceAuto
	}
	if r.Flash.Verify == "" {
		r.Flash.Verify = "readback-sha256"
	}
	if r.Windows != nil {
		if r.Windows.Firstboot.Mode == "" {
			r.Windows.Firstboot.Mode = "generate"
		}
		if r.Windows.Firstboot.Log == "" {
			r.Windows.Firstboot.Log = "firstboot.log"
		}
	}
}

// Validate enforces structural rules; production-lesson rules live in Lint.
func (r *Recipe) Validate() error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%s: %s", r.Path, fmt.Sprintf(format, args...))
	}
	switch {
	case r.Version != 1:
		return fail("version must be 1")
	case r.ID == "" || !idRe.MatchString(r.ID):
		return fail("id %q must be lowercase letters, digits, dot, dash, underscore", r.ID)
	case r.OS.Type != OSWindows && r.OS.Type != OSLinuxISO && r.OS.Type != OSRawImg:
		return fail("os.type must be windows, linux-iso, or raw-img")
	case r.Target.Boot != "uefi-only":
		return fail("target.boot must be uefi-only (legacy BIOS boot is out of scope)")
	case r.Target.Scheme != "mbr" && r.Target.Scheme != "gpt":
		return fail("target.scheme must be mbr or gpt")
	}
	if r.OS.Type == OSWindows {
		if r.Target.Filesystem != "fat32" {
			return fail("target.filesystem must be fat32 for Windows media (WIMs over 4 GiB get split, never NTFS)")
		}
		if r.Windows == nil {
			return fail("os.type windows requires a windows: section")
		}
		switch r.OS.SourceMode {
		case SourceAuto, SourceISO, SourceTree:
		default:
			return fail("os.source_mode must be auto, iso, or tree")
		}
		if r.OS.SourceMode == SourceTree && r.OS.TreePath == "" {
			return fail("os.source_mode tree requires os.tree_path")
		}
		if r.OS.Source == "" && r.OS.TreePath == "" {
			return fail("os.source (manifest id) or os.tree_path is required")
		}
		if d := r.Windows.Debloat; d != nil {
			switch d.Preset {
			case "off", "standard", "aggressive":
			default:
				return fail("windows.debloat.preset must be off, standard, or aggressive")
			}
		}
		fb := r.Windows.Firstboot
		if fb.Mode != "generate" && fb.Mode != "template" {
			return fail("windows.firstboot.mode must be generate or template")
		}
		for i, s := range fb.Steps {
			if s.Debloat && !r.Windows.Debloat.Enabled() {
				return fail("windows.firstboot.steps[%d] is `debloat` but windows.debloat is off/absent", i)
			}
		}
		if fb.Mode == "template" && fb.Template == "" {
			return fail("windows.firstboot.mode template requires windows.firstboot.template")
		}
		for i, p := range r.Windows.Payload {
			if (p.Ref == "") == (p.Path == "") {
				return fail("windows.payload[%d] needs exactly one of ref or path", i)
			}
		}
		for i, d := range r.Windows.DriverPacks {
			if (d.Ref == "") == (d.Path == "") {
				return fail("windows.driver_packs[%d] needs exactly one of ref or path", i)
			}
			switch d.Install {
			case InstallSweep, InstallExpandSweep, InstallExe:
			default:
				return fail("windows.driver_packs[%d] install must be pnputil-sweep, expand-then-sweep, or exe", i)
			}
		}
	} else {
		if r.Windows != nil {
			return fail("windows: section is only valid with os.type windows")
		}
		if r.OS.Source == "" {
			return fail("os.source is required")
		}
	}
	if r.Target.Size != "auto" {
		if _, err := ParseSize(r.Target.Size); err != nil {
			return fail("target.size: %v", err)
		}
	}
	if r.Target.MinStick != "" {
		if _, err := ParseSize(r.Target.MinStick); err != nil {
			return fail("target.min_stick: %v", err)
		}
	}
	if v := r.Flash.Verify; v != "readback-sha256" && v != "none" {
		return fail("flash.verify must be readback-sha256 or none")
	}
	return nil
}

var sizeRe = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*([KMGT]i?B|B)?$`)

// ParseSize parses "8GiB", "512MiB", "16GB" (decimal), or plain bytes.
func ParseSize(s string) (int64, error) {
	m := sizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("invalid size %q (use e.g. 8GiB, 512MiB, 16GB)", s)
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, err
	}
	mult := float64(1)
	switch m[2] {
	case "", "B":
	case "KiB":
		mult = 1 << 10
	case "MiB":
		mult = 1 << 20
	case "GiB":
		mult = 1 << 30
	case "TiB":
		mult = 1 << 40
	case "KB":
		mult = 1e3
	case "MB":
		mult = 1e6
	case "GB":
		mult = 1e9
	case "TB":
		mult = 1e12
	}
	return int64(val * mult), nil
}

// varRe matches ${var:name} references in recipe strings.
var varRe = regexp.MustCompile(`\$\{var:([A-Za-z0-9_.-]+)\}`)

// ExpandVars substitutes ${var:name} references from vars. Unknown names are
// an error so a missing secret fails the build instead of installing media
// with a literal placeholder.
func ExpandVars(s string, vars map[string]string) (string, error) {
	var missing []string
	out := varRe.ReplaceAllStringFunc(s, func(m string) string {
		name := varRe.FindStringSubmatch(m)[1]
		v, ok := vars[name]
		if !ok {
			missing = append(missing, name)
			return m
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("undefined var(s) %s — define in workspace vars, vars.local.yaml, or --var", strings.Join(missing, ", "))
	}
	return out, nil
}
