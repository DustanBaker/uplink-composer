// Package driverresolve finds the driver packs a machine needs and puts them
// in a workspace: it searches the vendor (Dell/Lenovo/HP) and Microsoft
// Update Catalog feeds, writes a pinned self-describing manifest per pack,
// and pulls the bytes into the library. It is the one implementation behind
// `dsky drivers search/resolve`, the `compose` build path, and Quick
// Install's hardware auto-detect.
package driverresolve

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/uplinkresearch/dsky/internal/drivers/catalog"
	"github.com/uplinkresearch/dsky/internal/fetch"
	"github.com/uplinkresearch/dsky/internal/hwdetect"
	"github.com/uplinkresearch/dsky/internal/library"
	"github.com/uplinkresearch/dsky/internal/manifest"
	"github.com/uplinkresearch/dsky/internal/recipe"
	"github.com/uplinkresearch/dsky/internal/workspace"
)

// Progress mirrors the compose/flash progress callback: a stage name plus
// byte counts, total -1 when indeterminate. A nil Progress discards.
type Progress func(stage string, done, total int64)

func (p Progress) stage(format string, args ...any) {
	if p != nil {
		p(fmt.Sprintf(format, args...), 0, -1)
	}
}

func (p Progress) bytes(stage string) func(done, total int64) {
	return func(done, total int64) {
		if p != nil {
			p(stage, done, total)
		}
	}
}

// resolverFeed is implemented by feeds whose results need a second request to
// learn the download URL (the Microsoft Update Catalog).
type resolverFeed interface {
	Resolve(ctx context.Context, p *catalog.Pack) error
}

// Resolved reports what one resolve pass covered.
type Resolved struct {
	// Specs are the input hardware entries reduced to what actually has a
	// pack, safe to write into a recipe: compose requires every
	// windows.hardware entry to match a manifest.
	Specs []recipe.HardwareSpec
	// Packs are the manifest ids staged, in order — newly downloaded and
	// already-present alike.
	Packs []string
	// Bytes is what those packs add to the media. GPU driver packages run to
	// a gigabyte each, so this decides how big a stick the build needs.
	Bytes int64
	// Missing describes hardware no feed covers (non-strict mode only).
	Missing []string
}

// SpecsFor turns a detected machine into the hardware entries worth
// resolving: the per-model pack when the maker has a feed (Dell/Lenovo/HP),
// plus GPU and NIC vendor+device IDs for the Microsoft Update Catalog.
func SpecsFor(h *hwdetect.Hardware, osName string) []recipe.HardwareSpec {
	if osName == "" {
		osName = "win11"
	}
	var out []recipe.HardwareSpec
	if v := h.KnownVendor(); v != "" && strings.TrimSpace(h.Model) != "" {
		out = append(out, recipe.HardwareSpec{Vendor: v, Model: strings.TrimSpace(h.Model), OS: osName})
	}
	if ids := h.DriverHWIDs(); len(ids) > 0 {
		out = append(out, recipe.HardwareSpec{HWIDs: ids, OS: osName})
	}
	return out
}

// SpecForModel parses a "vendor:model" target — a machine you are not sitting
// at, whose media you are building at the bench. The vendor is required and
// checked because only Dell, Lenovo and HP publish a per-model driver pack;
// anything else resolves per device, which needs the machine itself.
func SpecForModel(spec, osName string) (recipe.HardwareSpec, error) {
	if osName == "" {
		osName = "win11"
	}
	vendor, model, ok := strings.Cut(spec, ":")
	vendor = strings.ToLower(strings.TrimSpace(vendor))
	model = strings.TrimSpace(model)
	switch {
	case !ok || model == "":
		return recipe.HardwareSpec{}, fmt.Errorf("want \"vendor:model\", e.g. \"dell:OptiPlex 7010\" (got %q)", spec)
	case vendor != string(catalog.Dell) && vendor != string(catalog.Lenovo) && vendor != string(catalog.HP) && vendor != string(catalog.Framework):
		return recipe.HardwareSpec{}, fmt.Errorf("vendor must be dell, lenovo, hp or framework (got %q) — "+
			"other makers have no per-model feed, so build on the machine itself and detect it", vendor)
	}
	return recipe.HardwareSpec{Vendor: vendor, Model: model, OS: osName}, nil
}

// AddPack writes manifests/<id>.yaml for a catalog pack — its hardware
// binding and install method — and, unless told otherwise, pulls it into the
// library, pinning the SHA-256 for feeds that publish only a SHA-1.
func AddPack(ctx context.Context, ws *workspace.Workspace, lib *library.Library, feed catalog.Feed, p catalog.Pack, ref manifest.HardwareRef, pull bool, progress Progress) (string, error) {
	if r, ok := feed.(resolverFeed); ok && p.URL == "" {
		if err := r.Resolve(ctx, &p); err != nil {
			return "", err
		}
	}
	id := p.ID()
	install := recipe.InstallSweep
	switch p.Format {
	case "cab":
		install = recipe.InstallExpandSweep
	case "exe":
		install = recipe.InstallExtractSweep
	}
	if p.Install != "" {
		install = recipe.InstallMethod(p.Install)
	}
	manPath := filepath.Join(ws.Dir, "manifests", id+".yaml")
	if _, err := os.Stat(manPath); err != nil {
		if err := os.MkdirAll(filepath.Dir(manPath), 0o755); err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "id: %s\nkind: driver-pack\nformat: %s\n", id, p.Format)
		fmt.Fprintf(&b, "# Found in the %s driver catalog by `dsky drivers`.\n", feed.Vendor())
		fmt.Fprintf(&b, "url: %s\n", p.URL)
		fmt.Fprintf(&b, "sha256: %q\n", p.SHA256)
		if p.SHA1 != "" {
			fmt.Fprintf(&b, "sha1: %s\n", p.SHA1)
		}
		if p.Size > 0 {
			fmt.Fprintf(&b, "size: %d\n", p.Size)
		}
		fmt.Fprintf(&b, "filename: %s\n", filepath.Base(strings.SplitN(p.URL, "?", 2)[0]))
		fmt.Fprintf(&b, "hardware:\n")
		if ref.HWID != "" {
			fmt.Fprintf(&b, "  hwid: %q\n", ref.HWID)
		} else {
			fmt.Fprintf(&b, "  vendor: %s\n  model: %q\n", ref.Vendor, ref.Model)
		}
		fmt.Fprintf(&b, "  os: %s\n", ref.OS)
		fmt.Fprintf(&b, "install: %s\n", install)
		quote := func(list []string) string {
			quoted := make([]string, len(list))
			for i, a := range list {
				quoted[i] = fmt.Sprintf("%q", a)
			}
			return strings.Join(quoted, ", ")
		}
		if install == recipe.InstallExtractSweep {
			fmt.Fprintf(&b, "extract: [%s]\n", quote(p.Extract))
		}
		if install == recipe.InstallExe && len(p.Args) > 0 {
			fmt.Fprintf(&b, "args: [%s]\n", quote(p.Args))
		}
		note := strings.TrimSpace(fmt.Sprintf("%s %s %s %s", p.Model, p.OSVersion, p.Version, p.Released))
		fmt.Fprintf(&b, "notes: %q\n", note)
		if err := os.WriteFile(manPath, []byte(b.String()), 0o644); err != nil {
			return "", err
		}
		progress.stage("wrote manifests/%s.yaml", id)
	}
	if !pull {
		return id, nil
	}

	src, err := manifest.Load(manPath)
	if err != nil {
		return "", err
	}
	if _, err := lib.Resolve(id); err == nil {
		return id, nil
	}
	// pinTOFU is on: these manifests are written from catalog data, and feeds
	// without a SHA-256 are verified against their SHA-1 just below.
	entry, err := lib.Pull(ctx, src, true, nil, progress.bytes("downloading driver "+id))
	var unpinned *library.ErrUnpinned
	if err != nil && !errors.As(err, &unpinned) {
		return "", err
	}
	if entry.SHA256 == "" {
		return "", fmt.Errorf("%s: download produced no content hash", id)
	}
	if src.SHA256 == "" {
		if p.SHA1 != "" {
			got, err := fetch.SHA1File(lib.BlobPath(entry.SHA256))
			if err != nil {
				return "", err
			}
			if got != p.SHA1 {
				os.Remove(lib.BlobPath(entry.SHA256))
				return "", fmt.Errorf("%s: downloaded file SHA-1 %s does not match the catalog's %s — refusing", id, got, p.SHA1)
			}
		}
		text, err := os.ReadFile(manPath)
		if err != nil {
			return "", err
		}
		pinned := strings.Replace(string(text), "sha256: \"\"\n", "sha256: "+entry.SHA256+"\n", 1)
		if err := os.WriteFile(manPath, []byte(pinned), 0o644); err != nil {
			return "", err
		}
		progress.stage("pinned sha256 %s into manifests/%s.yaml", entry.SHA256, id)
	}
	return id, nil
}

// Resolve makes sure every hardware entry has a matching driver pack in the
// workspace and library: an existing manifest is reused, otherwise the right
// feed is searched and its newest pack for the OS is added.
//
// strict is for hand-authored recipes, where hardware naming a pack that
// cannot be found is a mistake worth stopping on. Non-strict is for
// auto-detect, where plenty of devices legitimately have no catalog pack
// (Windows ships or updates their drivers); those are dropped from the
// returned Specs and listed in Missing, because compose requires every
// hardware entry it is given to resolve to a pack.
func Resolve(ctx context.Context, ws *workspace.Workspace, lib *library.Library, hw []recipe.HardwareSpec, strict bool, progress Progress) (*Resolved, error) {
	out := &Resolved{}
	if len(hw) == 0 {
		return out, nil
	}
	sources, err := ws.Sources()
	if err != nil {
		return nil, err
	}
	have := func(vendor, model, hwid string) *manifest.Source {
		for _, s := range sources {
			if s.Hardware.Matches(vendor, model, hwid) {
				return s
			}
		}
		return nil
	}
	ensurePulled := func(s *manifest.Source) error {
		if _, err := lib.Resolve(s.ID); err == nil {
			return nil
		}
		_, err := lib.Pull(ctx, s, true, nil, progress.bytes("downloading driver "+s.ID))
		return err
	}
	cache := catalog.NewCache(lib.HelpersDir())

	for _, h := range hw {
		osName := h.OS
		if osName == "" {
			osName = "win11"
		}
		got := recipe.HardwareSpec{OS: osName}

		if h.Vendor != "" {
			switch id, err := resolveModel(ctx, ws, lib, cache, h, osName, have, ensurePulled, progress); {
			case err != nil && strict:
				return nil, err
			case err != nil:
				out.Missing = append(out.Missing, fmt.Sprintf("%s %s: %v", h.Vendor, h.Model, err))
			default:
				got.Vendor, got.Model = h.Vendor, h.Model
				if id != "" {
					out.Packs = append(out.Packs, id)
				}
			}
		}
		for _, hwid := range h.HWIDs {
			switch id, err := resolveHWID(ctx, ws, lib, cache, hwid, osName, have, ensurePulled, progress); {
			case err != nil && strict:
				return nil, err
			case err != nil:
				out.Missing = append(out.Missing, fmt.Sprintf("%s: %v", hwid, err))
			default:
				got.HWIDs = append(got.HWIDs, hwid)
				if id != "" {
					out.Packs = append(out.Packs, id)
				}
			}
		}
		if got.Vendor != "" || len(got.HWIDs) > 0 {
			out.Specs = append(out.Specs, got)
		}
	}
	for _, id := range out.Packs {
		if e, err := lib.Resolve(id); err == nil {
			out.Bytes += e.Size
		}
	}
	return out, nil
}

type findFunc func(vendor, model, hwid string) *manifest.Source
type pullFunc func(s *manifest.Source) error

// resolveModel covers one vendor+model entry, returning the id of the pack now
// staged for it.
func resolveModel(ctx context.Context, ws *workspace.Workspace, lib *library.Library, cache *catalog.Cache, h recipe.HardwareSpec, osName string, have findFunc, ensurePulled pullFunc, progress Progress) (string, error) {
	if s := have(h.Vendor, h.Model, ""); s != nil {
		return s.ID, ensurePulled(s)
	}
	feed, err := catalog.FeedFor(h.Vendor, cache)
	if err != nil {
		return "", err
	}
	progress.stage("finding %s drivers for %s", h.Vendor, h.Model)
	packs, err := feed.Search(ctx, catalog.Query{Model: h.Model, OS: osName})
	if err != nil {
		return "", err
	}
	if len(packs) == 0 {
		return "", fmt.Errorf("no %s driver pack for %q (%s) — check the model with `dsky drivers search %s %q`", h.Vendor, h.Model, osName, h.Vendor, h.Model)
	}
	ref := manifest.HardwareRef{Vendor: h.Vendor, Model: h.Model, OS: osName}
	return AddPack(ctx, ws, lib, feed, catalog.Exact(packs, h.Model), ref, true, progress)
}

// resolveHWID covers one hardware ID through the Microsoft Update Catalog.
func resolveHWID(ctx context.Context, ws *workspace.Workspace, lib *library.Library, cache *catalog.Cache, hwid, osName string, have findFunc, ensurePulled pullFunc, progress Progress) (string, error) {
	if s := have("", "", hwid); s != nil {
		return s.ID, ensurePulled(s)
	}
	feed, err := catalog.FeedFor("mscatalog", cache)
	if err != nil {
		return "", err
	}
	progress.stage("looking up %s in the Microsoft Update Catalog", hwid)
	packs, err := feed.Search(ctx, catalog.Query{HWID: hwid, OS: osName})
	if err != nil {
		return "", err
	}
	if len(packs) == 0 {
		return "", fmt.Errorf("the Microsoft Update Catalog has no %s driver", osName)
	}
	ref := manifest.HardwareRef{HWID: hwid, OS: osName}
	return AddPack(ctx, ws, lib, feed, packs[0], ref, true, progress)
}
