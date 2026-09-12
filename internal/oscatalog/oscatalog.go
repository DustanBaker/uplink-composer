// Package oscatalog is the built-in list of installable operating systems and
// the Quick-Install flow: pick an OS, choose a few options, and build media
// with no workspace to author. It synthesizes an ephemeral workspace and
// recipe and runs them through the normal compose pipeline.
package oscatalog

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DustanBaker/uplink-composer/internal/appcatalog"
	"github.com/DustanBaker/uplink-composer/internal/compose"
	"github.com/DustanBaker/uplink-composer/internal/driverresolve"
	"github.com/DustanBaker/uplink-composer/internal/helpers"
	"github.com/DustanBaker/uplink-composer/internal/library"
	"github.com/DustanBaker/uplink-composer/internal/manifest"
	"github.com/DustanBaker/uplink-composer/internal/recipe"
	"github.com/DustanBaker/uplink-composer/internal/workspace"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

// Family groups OSes.
type Family string

const (
	Windows Family = "windows"
	Linux   Family = "linux"
)

// Category groups the catalog for the pickers. A flat list stops being
// browsable somewhere around a dozen entries, and "I want a server" is a
// different errand from "I want to try a desktop".
type Category string

const (
	Desktop   Category = "desktop"
	Server    Category = "server"
	Appliance Category = "appliance" // single-board and purpose-built images
)

// ImageKind says how the downloaded file is written. Most installers are
// hybrid ISOs; single-board images are raw disk images, usually compressed,
// which take a different compose path.
type ImageKind string

const (
	ImageISO ImageKind = "iso"
	ImageRaw ImageKind = "raw"
)

// Entry is one installable OS in the built-in catalog.
type Entry struct {
	ID            string
	Name          string
	Family        Family
	Category      Category
	Version       string
	Notes         string
	FirmwareNotes string

	// Image defaults to ImageISO. Arch defaults to amd64 and exists so an
	// arm64 board image is never offered as if it would boot a PC.
	Image ImageKind
	Arch  string

	// Windows sources resolve their ISO at pull time via Fido; Linux
	// sources pin url + sha256 (or a ChecksumsURL to verify against).
	Provider     string
	Fido         *manifest.FidoSpec
	URL          string
	SHA256       string
	ChecksumsURL string // fetch + verify when SHA256 is empty
	Filename     string

	// Windows-only options.
	Editions []string // e.g. Pro, Home
}

// Options are the Quick-Install choices.
type Options struct {
	Edition           string // windows: Pro | Home
	AccountMode       string // windows: local | oobe
	Debloat           string // windows: off | standard | aggressive
	BypassRequirement bool   // windows: skip TPM/SecureBoot/RAM checks
	// Hardware asks for driver packs to be found and staged for these
	// machines (windows only) — normally what hwdetect found on this box,
	// via driverresolve.SpecsFor. Entries no catalog covers are dropped.
	Hardware []recipe.HardwareSpec
	// Apps are appcatalog picker ids to install at first boot (windows only,
	// via winget). Resolved to package ids by the caller.
	Apps []string
}

// sourceFormat is the manifest format for this entry's download. Raw images
// arrive compressed (.img.xz); the flash engine sniffs and decompresses on
// the way to the device, so the manifest just says "img".
func (e Entry) sourceFormat() manifest.Format {
	if e.Kind() == ImageRaw {
		return manifest.FormatImg
	}
	return manifest.FormatISO
}

// recipeOSType is the compose pipeline this entry runs through.
func (e Entry) recipeOSType() string {
	if e.Kind() == ImageRaw {
		return "raw-img"
	}
	return "linux-iso"
}

// ImageKind is the entry's image kind, defaulting to a hybrid ISO.
func (e Entry) Kind() ImageKind {
	if e.Image != "" {
		return e.Image
	}
	return ImageISO
}

// CPUArch is the entry's architecture, defaulting to amd64.
func (e Entry) CPUArch() string {
	if e.Arch != "" {
		return e.Arch
	}
	return "amd64"
}

// Group is the entry's category, defaulting by family: Windows and Linux
// entries that do not say otherwise are desktops.
func (e Entry) Group() Category {
	if e.Category != "" {
		return e.Category
	}
	return Desktop
}

// DriverOS is the driver-catalog OS token for this entry ("win11"/"win10").
func (e Entry) DriverOS() string {
	if e.Fido != nil && e.Fido.Win == "10" {
		return "win10"
	}
	return "win11"
}

// wingetPkgs resolves the picker ids to winget package ids. The error is
// dropped on purpose: BuildQuick validates the same list up front and refuses
// the build, so by the time the recipe is rendered these all resolve.
func (o Options) wingetPkgs() []string {
	pkgs, _ := appcatalog.WingetIDs(o.Apps)
	return pkgs
}

func (o *Options) defaults(e Entry) {
	if o.Edition == "" && len(e.Editions) > 0 {
		o.Edition = e.Editions[0]
	}
	if o.AccountMode == "" {
		o.AccountMode = "local"
	}
	if o.Debloat == "" {
		o.Debloat = "standard"
	}
}

// genericKeys are Microsoft's public edition-select keys (they choose the
// edition Setup installs; activation still needs a real license).
var genericKeys = map[string]string{
	"Pro":        "VK7JG-NPHTM-C97JM-9MPGT-3V66T",
	"Home":       "YTMG3-N6DKC-DKB77-7M9GH-8HVX7",
	"Pro N":      "2B87N-8KFHP-DKV6R-Y2C8J-PKCKT",
	"Education":  "YNMGQ-8RYV3-4PGQ3-C8XTP-7CFBY",
	"Enterprise": "XGVPP-NMH47-7TTHJ-W3FW7-8HV2C",
}

// Catalog returns the built-in OS list.
func Catalog() []Entry { return builtin }

// Get returns the entry with id, or false.
func Get(id string) (Entry, bool) {
	for _, e := range builtin {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// BuildQuick pulls the OS (if needed), synthesizes an ephemeral workspace and
// recipe from the entry + options, and composes flashable media.
func BuildQuick(ctx context.Context, lib *library.Library, e Entry, opts Options, progress func(stage string, done, total int64)) (*compose.Artifact, error) {
	opts.defaults(e)
	if e.Family == Windows && genericKeys[opts.Edition] == "" {
		return nil, fmt.Errorf("unknown Windows edition %q (have: %s)", opts.Edition, strings.Join(e.Editions, ", "))
	}
	if len(opts.Apps) > 0 {
		if e.Family != Windows {
			return nil, fmt.Errorf("installing programs alongside %s is not supported yet — it needs an autoinstall recipe", e.Name)
		}
		// Fail before downloading gigabytes, not after.
		if _, err := appcatalog.WingetIDs(opts.Apps); err != nil {
			return nil, err
		}
	}

	if err := ensureSource(ctx, lib, e, progress); err != nil {
		return nil, err
	}
	wsDir, err := scaffoldQuickWorkspace(lib, e, opts, nil)
	if err != nil {
		return nil, err
	}
	ws, err := workspace.Load(wsDir)
	if err != nil {
		return nil, err
	}

	// Driver auto-resolve rewrites the recipe, because compose requires every
	// windows.hardware entry to match a staged pack: a detected GPU the
	// catalogs don't carry (common — Windows Update covers most) must not
	// fail the build, so only what resolved goes in.
	if e.Family == Windows && len(opts.Hardware) > 0 {
		res, err := driverresolve.Resolve(ctx, ws, lib, opts.Hardware, false, progress)
		if err != nil {
			return nil, err
		}
		if progress != nil {
			for _, m := range res.Missing {
				progress("no driver pack found for "+m, 0, -1)
			}
		}
		if len(res.Specs) > 0 {
			if err := writeQuickRecipe(wsDir, e, opts, res.Specs); err != nil {
				return nil, err
			}
			if ws, err = workspace.Load(wsDir); err != nil {
				return nil, err
			}
		}
	}

	r, err := ws.Recipe(e.ID)
	if err != nil {
		return nil, err
	}
	return compose.Build(ctx, compose.Request{
		Workspace: ws, Library: lib, Recipe: r,
		Progress: progress,
	})
}

// InLibrary reports whether this entry's OS image is already local, so a
// caller can skip both the fetch and a re-import.
func InLibrary(lib *library.Library, e Entry) bool {
	_, err := lib.Resolve(e.ID)
	return err == nil
}

// CheckISO validates an ISO path before any expensive work starts, so a typo
// fails immediately rather than after "hashing several GB" has been announced.
func CheckISO(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("%s is a directory; point --iso at the .iso file", path)
	}
	if !strings.EqualFold(filepath.Ext(path), ".iso") {
		return fmt.Errorf("%s is not a .iso", filepath.Base(path))
	}
	if st.Size() < 1<<30 {
		return fmt.Errorf("%s is only %d MiB — that is too small to be an OS installer ISO",
			filepath.Base(path), st.Size()>>20)
	}
	return nil
}

// ImportISO files an ISO the operator downloaded themselves under this
// entry's id, so Quick Install uses it instead of fetching. Microsoft
// rate-limits the on-demand Windows fetch hard enough (roughly one per day
// per address) that "download it yourself once" is a normal path, not a
// fallback — and there is otherwise no way to hand Quick Install an ISO,
// since `sources import` needs a workspace and a manifest.
func ImportISO(lib *library.Library, e Entry, path string) (library.Entry, error) {
	if err := CheckISO(path); err != nil {
		return library.Entry{}, err
	}
	src := &manifest.Source{
		ID: e.ID, Kind: manifest.KindOSImage, Format: manifest.FormatISO,
		Filename: filepath.Base(path),
	}
	return lib.Import(src, path)
}

// PlanDrivers finds and downloads the driver packs a machine needs without
// building any media, reporting what each piece of hardware resolved to. It
// stages into the same Quick Install workspace the real build uses, so
// nothing is fetched twice.
func PlanDrivers(ctx context.Context, lib *library.Library, e Entry, hw []recipe.HardwareSpec, progress func(stage string, done, total int64)) (*driverresolve.Resolved, error) {
	opts := Options{}
	opts.defaults(e)
	wsDir, err := scaffoldQuickWorkspace(lib, e, opts, nil)
	if err != nil {
		return nil, err
	}
	ws, err := workspace.Load(wsDir)
	if err != nil {
		return nil, err
	}
	return driverresolve.Resolve(ctx, ws, lib, hw, false, progress)
}

// ensureSource makes sure the OS ISO is in the library.
func ensureSource(ctx context.Context, lib *library.Library, e Entry, progress func(string, int64, int64)) error {
	if _, err := lib.Resolve(e.ID); err == nil {
		return nil
	}
	src := &manifest.Source{
		ID: e.ID, Kind: manifest.KindOSImage, Format: e.sourceFormat(),
		Provider: e.Provider, Fido: e.Fido, URL: e.URL, SHA256: e.SHA256, Filename: e.Filename,
	}
	// Distros that publish a SHA256SUMS file: resolve the pin now.
	if src.SHA256 == "" && e.ChecksumsURL != "" {
		sum, err := resolveChecksum(ctx, e)
		if err != nil {
			return err
		}
		src.SHA256 = sum
	}
	resolver := func(ctx context.Context, s *manifest.Source) (string, error) {
		if progress != nil {
			progress("resolving download URL", 0, -1)
		}
		return helpers.ResolveFidoURL(ctx, lib.HelpersDir(), s.Fido)
	}
	// Windows (Fido, unpinnable) uses trust-on-first-use; pinned distros verify.
	_, err := lib.Pull(ctx, src, true, resolver, func(done, total int64) {
		if progress != nil {
			progress("downloading "+e.Name, done, total)
		}
	})
	return err
}

// scaffoldQuickWorkspace writes an ephemeral workspace under the library with
// the embedded template, a manifest for the OS, and a synthesized recipe.
func scaffoldQuickWorkspace(lib *library.Library, e Entry, opts Options, hw []recipe.HardwareSpec) (string, error) {
	dir := filepath.Join(lib.Root, "quick")
	for _, sub := range []string{"templates", "manifests", "recipes", "payload"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "workspace.yaml"),
		[]byte("version: 1\norg:\n  name: \"Quick Install\"\n  id: quick\ndefaults:\n  locale: en-US\n"), 0o644); err != nil {
		return "", err
	}
	tmpl, err := templatesFS.ReadFile("templates/autounattend.xml.tmpl")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "templates", "autounattend.xml.tmpl"), tmpl, 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifests", e.ID+".yaml"), []byte(manifestYAML(e)), 0o644); err != nil {
		return "", err
	}
	if err := writeQuickRecipe(dir, e, opts, hw); err != nil {
		return "", err
	}
	// Only keep this build's recipe so ws.Recipes() stays unambiguous.
	pruneOtherRecipes(filepath.Join(dir, "recipes"), e.ID)
	pruneOtherManifests(filepath.Join(dir, "manifests"), e.ID)
	return dir, nil
}

func writeQuickRecipe(dir string, e Entry, opts Options, hw []recipe.HardwareSpec) error {
	return os.WriteFile(filepath.Join(dir, "recipes", e.ID+".yaml"), []byte(recipeYAML(e, opts, hw)), 0o644)
}

func pruneOtherRecipes(dir, keep string) {
	entries, _ := os.ReadDir(dir)
	for _, en := range entries {
		if en.Name() != keep+".yaml" {
			os.Remove(filepath.Join(dir, en.Name()))
		}
	}
}

// pruneOtherManifests drops a previous Quick Install's OS manifest but keeps
// resolved driver packs: they are pinned, already downloaded, and only ever
// matched by hardware, so keeping them makes repeat installs on the same
// machine skip the catalog lookups entirely.
func pruneOtherManifests(dir, keep string) {
	entries, _ := os.ReadDir(dir)
	for _, en := range entries {
		if en.Name() == keep+".yaml" {
			continue
		}
		p := filepath.Join(dir, en.Name())
		if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), "kind: driver-pack") {
			continue
		}
		os.Remove(p)
	}
}

func manifestYAML(e Entry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "id: %s\nkind: os-image\nformat: %s\n", e.ID, e.sourceFormat())
	if e.Provider != "" {
		fmt.Fprintf(&b, "provider: %s\n", e.Provider)
		if e.Fido != nil {
			fmt.Fprintf(&b, "fido:\n  win: %q\n  release: %s\n  edition: %s\n  language: %s\n  arch: %s\n",
				e.Fido.Win, e.Fido.Release, e.Fido.Edition, e.Fido.Language, e.Fido.Arch)
		}
	} else {
		fmt.Fprintf(&b, "url: %s\n", e.URL)
	}
	fmt.Fprintf(&b, "sha256: %q\n", e.SHA256)
	if e.Filename != "" {
		fmt.Fprintf(&b, "filename: %s\n", e.Filename)
	}
	return b.String()
}

// hardwareYAML renders resolved machines as a windows.hardware block. Vendor
// packs and hardware IDs become separate entries, which is how compose
// matches them against staged manifests.
func hardwareYAML(hw []recipe.HardwareSpec) string {
	if len(hw) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("  hardware:\n")
	for _, h := range hw {
		osName := h.OS
		if osName == "" {
			osName = "win11"
		}
		if h.Vendor != "" {
			fmt.Fprintf(&b, "    - { vendor: %s, model: %q, os: %s }\n", h.Vendor, h.Model, osName)
		}
		if len(h.HWIDs) > 0 {
			quoted := make([]string, len(h.HWIDs))
			for i, id := range h.HWIDs {
				quoted[i] = fmt.Sprintf("%q", id)
			}
			fmt.Fprintf(&b, "    - { hwids: [%s], os: %s }\n", strings.Join(quoted, ", "), osName)
		}
	}
	return b.String()
}

func recipeYAML(e Entry, opts Options, hw []recipe.HardwareSpec) string {
	if e.Family == Linux {
		// Raw images arrive compressed and expand several times over, so the
		// stick they need is much bigger than the download suggests.
		minStick := "4GiB"
		if e.Kind() == ImageRaw {
			minStick = "8GiB"
		}
		return fmt.Sprintf(`version: 1
id: %s
name: %q
os:
  type: %s
  source: %s
target:
  min_stick: %s
  boot: uefi-only
flash:
  verify: readback-sha256
`, e.ID, e.Name, e.recipeOSType(), e.ID, minStick)
	}
	bypass := "0"
	if opts.BypassRequirement {
		bypass = "1"
	}
	preset := debloatPreset(opts.Debloat)
	steps := "      - drivers\n"
	if preset != "off" {
		steps += "      - debloat\n"
	}
	appsBlock := ""
	if pkgs := opts.wingetPkgs(); len(pkgs) > 0 {
		var b strings.Builder
		b.WriteString("  apps:\n    winget:\n")
		for _, p := range pkgs {
			fmt.Fprintf(&b, "      - %s\n", p)
		}
		appsBlock = b.String()
		steps += "      - apps\n"
	}
	// Staged GPU driver packages run past a gigabyte each, so driver media
	// outgrows the 8 GiB stick a bare Windows ISO fits on.
	minStick := "8GiB"
	if len(hw) > 0 {
		minStick = "16GiB"
	}
	return fmt.Sprintf(`version: 1
id: %s
name: %q
os:
  type: windows
  source: %s
  source_mode: iso
target:
  scheme: mbr
  filesystem: fat32
  volume_label: ESD-USB
  size: auto
  min_stick: %s
  boot: uefi-only
windows:
  ei_cfg: { edition: %s, channel: Retail, vl: false }
  unattend:
    template: templates/autounattend.xml.tmpl
    vars:
      edition_key: %s
      locale: en-US
      admin_user: user
      admin_display_name: User
      admin_password: ""
      computer_name: "*"
      account_mode: %s
      bypass_requirements: "%s"
%s  debloat:
    preset: %s
%s  firstboot:
    mode: generate
    steps:
%s
flash:
  verify: readback-sha256
`, e.ID, e.Name, e.ID, minStick, editionName(opts.Edition), genericKeys[opts.Edition],
		opts.AccountMode, bypass, hardwareYAML(hw), preset, appsBlock, steps)
}

// editionName maps the option to the ei.cfg EditionID (drops the "N"/space).
func editionName(ed string) string {
	switch ed {
	case "Pro N":
		return "ProfessionalN"
	case "Pro":
		return "Professional"
	case "Home":
		return "Core"
	case "Education":
		return "Education"
	case "Enterprise":
		return "Enterprise"
	}
	return "Professional"
}

func debloatPreset(d string) string {
	if d == "" {
		return "off"
	}
	return d
}
