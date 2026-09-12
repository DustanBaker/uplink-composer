package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DustanBaker/uplink-composer/internal/drivers/catalog"
	"github.com/DustanBaker/uplink-composer/internal/fetch"
	"github.com/DustanBaker/uplink-composer/internal/library"
	"github.com/DustanBaker/uplink-composer/internal/manifest"
	"github.com/DustanBaker/uplink-composer/internal/recipe"
	"github.com/DustanBaker/uplink-composer/internal/workspace"
)

// driversSearch queries a vendor catalog (dell/lenovo/hp by model) or the
// Microsoft Update Catalog (by hardware ID) and optionally adds a result
// to the workspace as a pinned, self-describing driver-pack manifest.
func driversSearch(ctx context.Context, env *Env, args []string) error {
	fs := flag.NewFlagSet("drivers search", flag.ContinueOnError)
	osName := fs.String("os", "win11", "target OS: win11 | win10")
	limit := fs.Int("limit", 15, "max results to show")
	add := fs.Bool("add", false, "add a result to the workspace (manifest + library pull)")
	pick := fs.Int("pick", 1, "which result --add takes (1 = first)")
	noPull := fs.Bool("no-pull", false, "with --add: write the manifest but don't download yet")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("drivers search <dell|lenovo|hp|mscatalog> <model or hardware-id> [--add] [--pick N]")
	}
	lib, err := env.library()
	if err != nil {
		return err
	}
	feed, err := catalog.FeedFor(fs.Arg(0), catalog.NewCache(lib.HelpersDir()))
	if err != nil {
		return err
	}
	q := catalog.Query{OS: *osName}
	if feed.Vendor() == catalog.MSCatalog {
		q.HWID = fs.Arg(1)
	} else {
		q.Model = fs.Arg(1)
	}
	fmt.Printf("searching %s for %q (%s)...\n", feed.Vendor(), fs.Arg(1), *osName)
	packs, err := feed.Search(ctx, q)
	if err != nil {
		return err
	}
	if len(packs) == 0 {
		return fmt.Errorf("no %s driver packs match %q for %s", feed.Vendor(), fs.Arg(1), *osName)
	}
	printPacks(packs, *limit)
	if !*add {
		fmt.Printf("\nAdd one:  uplink drivers search %s %q --add --pick N\n", fs.Arg(0), fs.Arg(1))
		return nil
	}
	if *pick < 1 || *pick > len(packs) {
		return fmt.Errorf("--pick %d is out of range (1..%d)", *pick, len(packs))
	}
	ws, err := env.workspace()
	if err != nil {
		return err
	}
	ref := manifest.HardwareRef{OS: *osName}
	if feed.Vendor() == catalog.MSCatalog {
		ref.HWID = fs.Arg(1)
	} else {
		ref.Vendor = string(feed.Vendor())
		ref.Model = packs[*pick-1].Model
	}
	id, err := addPack(ctx, ws, lib, feed, packs[*pick-1], ref, !*noPull)
	if err != nil {
		return err
	}
	fmt.Printf("\nAdded %s. A recipe picks it up via:\n", id)
	if ref.HWID != "" {
		fmt.Printf("  windows:\n    hardware:\n      - { hwids: [%q] }\n", ref.HWID)
	} else {
		fmt.Printf("  windows:\n    hardware:\n      - { vendor: %s, model: %q }\n", ref.Vendor, ref.Model)
	}
	return nil
}

func printPacks(packs []catalog.Pack, limit int) {
	for i, p := range packs {
		if i >= limit {
			fmt.Printf("  … %d more (raise --limit)\n", len(packs)-limit)
			break
		}
		ver := p.OSVersion
		if ver == "" || ver == "*" {
			ver = p.OS
		}
		size := "?"
		if p.Size > 0 {
			size = fmt.Sprintf("%d MiB", p.Size>>20)
			if p.Size < 1<<20 {
				size = fmt.Sprintf("%d KiB", p.Size>>10)
			}
		}
		fmt.Printf("  %2d. %-46s %-6s %-10s %-9s %-4s %s\n", i+1, trunc(p.Model, 46), ver, p.Released, size, p.Format, trunc(p.Version, 14))
	}
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// resolverFeed is implemented by feeds whose results need a second request
// to learn the download URL (the Microsoft Update Catalog).
type resolverFeed interface {
	Resolve(ctx context.Context, p *catalog.Pack) error
}

// addPack writes manifests/<id>.yaml for the pack (with its hardware
// binding and install method) and, unless told otherwise, pulls it into the
// library — pinning the SHA-256 for feeds that don't publish one.
func addPack(ctx context.Context, ws *workspace.Workspace, lib *library.Library, feed catalog.Feed, p catalog.Pack, ref manifest.HardwareRef, pull bool) (string, error) {
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
	manPath := filepath.Join(ws.Dir, "manifests", id+".yaml")
	if _, err := os.Stat(manPath); err != nil {
		if err := os.MkdirAll(filepath.Dir(manPath), 0o755); err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "id: %s\nkind: driver-pack\nformat: %s\n", id, p.Format)
		fmt.Fprintf(&b, "# Found by `uplink drivers search %s` on %s catalog data.\n", feed.Vendor(), feed.Vendor())
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
		if install == recipe.InstallExtractSweep {
			quoted := make([]string, len(p.Extract))
			for i, a := range p.Extract {
				quoted[i] = fmt.Sprintf("%q", a)
			}
			fmt.Fprintf(&b, "extract: [%s]\n", strings.Join(quoted, ", "))
		}
		note := strings.TrimSpace(fmt.Sprintf("%s %s %s %s", p.Model, p.OSVersion, p.Version, p.Released))
		fmt.Fprintf(&b, "notes: %q\n", note)
		if err := os.WriteFile(manPath, []byte(b.String()), 0o644); err != nil {
			return "", err
		}
		fmt.Printf("wrote manifests/%s.yaml\n", id)
	} else {
		fmt.Printf("manifests/%s.yaml already exists — keeping it\n", id)
	}
	if !pull {
		return id, nil
	}

	src, err := manifest.Load(manPath)
	if err != nil {
		return "", err
	}
	if _, err := lib.Resolve(id); err == nil {
		fmt.Printf("%s already in library\n", id)
		return id, nil
	}
	fmt.Printf("pulling %s (%d MiB)...\n", id, p.Size>>20)
	prog := &stageProgress{}
	entry, err := lib.Pull(ctx, src, true, nil, func(done, total int64) { prog.report("download", done, total) })
	prog.finish()
	var unpinned *library.ErrUnpinned
	if err != nil && !errors.As(err, &unpinned) {
		return "", err
	}
	// Feeds without a SHA-256 (the Microsoft Update Catalog) publish a
	// SHA-1: verify the download against it, then pin the SHA-256 we saw.
	if src.SHA256 == "" {
		if p.SHA1 != "" {
			got, err := sha1File(lib.BlobPath(entry.SHA256))
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
		fmt.Printf("pinned sha256 %s into manifests/%s.yaml\n", entry.SHA256, id)
	}
	fmt.Printf("%s in library (%d MiB)\n", id, entry.Size>>20)
	return id, nil
}

// resolveHardware makes sure every windows.hardware entry of the recipe has
// a matching pulled driver pack: searches the right feed, takes the newest
// pack for the OS, writes its manifest, and pulls it. Used by
// `drivers resolve` and automatically by `compose <recipe>`.
func resolveHardware(ctx context.Context, ws *workspace.Workspace, lib *library.Library, r *recipe.Recipe) error {
	if r.Windows == nil || len(r.Windows.Hardware) == 0 {
		return nil
	}
	sources, err := ws.Sources()
	if err != nil {
		return err
	}
	cache := catalog.NewCache(lib.HelpersDir())
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
		fmt.Printf("pulling %s...\n", s.ID)
		prog := &stageProgress{}
		_, err := lib.Pull(ctx, s, true, nil, func(done, total int64) { prog.report("download", done, total) })
		prog.finish()
		return err
	}
	for _, h := range r.Windows.Hardware {
		osName := h.OS
		if osName == "" {
			osName = "win11"
		}
		if h.Vendor != "" {
			if s := have(h.Vendor, h.Model, ""); s != nil {
				if err := ensurePulled(s); err != nil {
					return err
				}
				continue
			}
			feed, err := catalog.FeedFor(h.Vendor, cache)
			if err != nil {
				return err
			}
			fmt.Printf("resolving drivers for %s %q (%s)...\n", h.Vendor, h.Model, osName)
			packs, err := feed.Search(ctx, catalog.Query{Model: h.Model, OS: osName})
			if err != nil {
				return err
			}
			if len(packs) == 0 {
				return fmt.Errorf("no %s driver pack found for %q (%s) — check the model name with `uplink drivers search %s %q`", h.Vendor, h.Model, osName, h.Vendor, h.Model)
			}
			ref := manifest.HardwareRef{Vendor: h.Vendor, Model: h.Model, OS: osName}
			if _, err := addPack(ctx, ws, lib, feed, packs[0], ref, true); err != nil {
				return err
			}
		}
		for _, hwid := range h.HWIDs {
			if s := have("", "", hwid); s != nil {
				if err := ensurePulled(s); err != nil {
					return err
				}
				continue
			}
			feed, _ := catalog.FeedFor("mscatalog", cache)
			fmt.Printf("resolving driver for %s (%s) via the Microsoft Update Catalog...\n", hwid, osName)
			packs, err := feed.Search(ctx, catalog.Query{HWID: hwid, OS: osName})
			if err != nil {
				return err
			}
			if len(packs) == 0 {
				return fmt.Errorf("the Microsoft Update Catalog has no %s driver for %s", osName, hwid)
			}
			ref := manifest.HardwareRef{HWID: hwid, OS: osName}
			if _, err := addPack(ctx, ws, lib, feed, packs[0], ref, true); err != nil {
				return err
			}
		}
	}
	return nil
}

func driversResolve(ctx context.Context, env *Env, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("drivers resolve <recipe-id>")
	}
	ws, err := env.workspace()
	if err != nil {
		return err
	}
	lib, err := env.library()
	if err != nil {
		return err
	}
	r, err := ws.Recipe(args[0])
	if err != nil {
		return err
	}
	if r.Windows == nil || len(r.Windows.Hardware) == 0 {
		fmt.Println("recipe has no windows.hardware entries — nothing to resolve")
		return nil
	}
	if err := resolveHardware(ctx, ws, lib, r); err != nil {
		return err
	}
	fmt.Println("all hardware entries have driver packs in the library — build with: compose", r.ID)
	return nil
}

func sha1File(path string) (string, error) {
	return fetch.SHA1File(path)
}
