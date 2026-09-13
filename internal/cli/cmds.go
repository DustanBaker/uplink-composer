package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/uplinkresearch/bootwright/internal/compose"
	"github.com/uplinkresearch/bootwright/internal/device"
	"github.com/uplinkresearch/bootwright/internal/helpers"
	"github.com/uplinkresearch/bootwright/internal/library"
	"github.com/uplinkresearch/bootwright/internal/manifest"
	"github.com/uplinkresearch/bootwright/internal/workspace"
)

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	org := fs.String("org", "", "organization name (required)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	if *org == "" {
		return fmt.Errorf("init: --org \"Org Name\" is required")
	}
	if err := workspace.Scaffold(dir, *org); err != nil {
		return err
	}
	abs, _ := filepath.Abs(dir)
	fmt.Printf("Workspace for %q created at %s\n", *org, abs)
	fmt.Println("Next: edit recipes/example-win11.yaml, add manifests, then `bootwright build example-win11`.")
	fmt.Println("Commit the workspace to a private git repo; vars.local.yaml stays out (gitignored).")
	return nil
}

func cmdDoctor(ctx context.Context, env *Env) error {
	lib, err := env.library()
	if err != nil {
		return err
	}
	fmt.Println("Bootwright — doctor")
	fmt.Println()
	fmt.Printf("library root:  %s\n", lib.Root)
	if strings.Contains(strings.ToLower(lib.Root), "onedrive") {
		fmt.Println("  WARNING: library sits under a OneDrive-synced path; multi-GB blobs will sync — set BOOTWRIGHT_LIBRARY elsewhere")
	}
	if entries, err := lib.List(); err == nil {
		var total int64
		for _, e := range entries {
			total += e.Size
		}
		fmt.Printf("library:       %d source(s), %d MiB (catalog %s)\n", len(entries), total>>20, filepath.Join(lib.Root, "catalog.json"))
	} else {
		fmt.Printf("library:       ERROR reading catalog: %v\n", err)
	}
	if metas, err := filepath.Glob(filepath.Join(lib.ArtifactsDir(), "*.img.json")); err == nil {
		fmt.Printf("artifacts:     %d built image(s)\n", len(metas))
	}
	fmt.Println()
	fmt.Println("required tools on this host:")
	for _, st := range helpers.Check(lib.HelpersDir()) {
		mark := "ok  "
		detail := st.Path
		if st.Path == "" {
			mark = "MISS"
			detail = "not found — " + st.Hint
		}
		fmt.Printf("  [%s] %-16s %-26s %s\n", mark, st.Name, st.Purpose, detail)
	}
	fmt.Println()
	if ws, err := env.workspace(); err == nil {
		fmt.Printf("workspace:     %s (org %q)\n", ws.Dir, ws.Config.Org.Name)
		if rs, err := ws.Recipes(); err == nil {
			fmt.Printf("recipes:       %d\n", len(rs))
		} else {
			fmt.Printf("recipes:       ERROR %v\n", err)
		}
	} else {
		fmt.Printf("workspace:     none found from %s (bootwright init)\n", env.WorkspaceDir)
	}
	devs, err := device.List(ctx)
	if err != nil {
		fmt.Printf("devices:       ERROR %v\n", err)
	} else {
		flashable := 0
		for _, d := range devs {
			if d.Flashable() {
				flashable++
			}
		}
		fmt.Printf("devices:       %d disks visible, %d flashable\n", len(devs), flashable)
	}
	if runtime.GOOS == "windows" {
		fmt.Println("note: flashing pops one UAC prompt per stick unless run from an Administrator terminal")
	}
	return nil
}

func cmdDevices(ctx context.Context) error {
	devs, err := device.List(ctx)
	if err != nil {
		return err
	}
	if len(devs) == 0 {
		fmt.Println("no disks found")
		return nil
	}
	fmt.Println("Candidate targets (flashable first):")
	for _, d := range devs {
		fmt.Println(" ", d.String())
	}
	fmt.Println("\nFlash with: bootwright flash <recipe> <device-id>")
	return nil
}

func cmdSources(ctx context.Context, env *Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("sources: need list, pull, or import")
	}
	lib, err := env.library()
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		ws, err := env.workspace()
		if err != nil {
			return err
		}
		srcs, err := ws.Sources()
		if err != nil {
			return err
		}
		if len(srcs) == 0 {
			fmt.Println("no manifests in", filepath.Join(ws.Dir, "manifests"))
			return nil
		}
		for _, s := range srcs {
			status := "not in library"
			if e, err := lib.Resolve(s.ID); err == nil {
				status = fmt.Sprintf("in library (%d MiB)", e.Size>>20)
			}
			pin := "pinned"
			if s.SHA256 == "" {
				pin = "UNPINNED"
			}
			fmt.Printf("  %-28s %-12s %-9s %s\n", s.ID, s.Kind, pin, status)
		}
		return nil
	case "pull":
		fs := flag.NewFlagSet("pull", flag.ContinueOnError)
		tofu := fs.Bool("pin-tofu", false, "trust-on-first-use: catalog an unpinned source and print the hash to pin")
		if err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return fmt.Errorf("sources pull <id> [--pin-tofu]")
		}
		ws, err := env.workspace()
		if err != nil {
			return err
		}
		src, err := ws.Source(fs.Arg(0))
		if err != nil {
			return err
		}
		prog := &stageProgress{}
		resolver := func(ctx context.Context, s *manifest.Source) (string, error) {
			prog.report("resolving Microsoft download URL (Fido)", 0, -1)
			return helpers.ResolveFidoURL(ctx, lib.HelpersDir(), s.Fido)
		}
		entry, err := lib.Pull(ctx, src, *tofu, resolver, func(done, total int64) {
			prog.report("download", done, total)
		})
		prog.finish()
		var unpinned *library.ErrUnpinned
		if errors.As(err, &unpinned) {
			fmt.Printf("downloaded; sha256 = %s\n", unpinned.SHA256)
			return err
		}
		if err != nil {
			return err
		}
		fmt.Printf("%s in library: %s (%d MiB, sha256 %s)\n", entry.ID, entry.Filename, entry.Size>>20, entry.SHA256)
		if src.SHA256 == "" {
			fmt.Printf("PIN IT: add `sha256: %s` to the manifest so other machines verify this exact content\n", entry.SHA256)
		}
		return nil
	case "import":
		if len(args) != 3 {
			return fmt.Errorf("sources import <id> <file>")
		}
		ws, err := env.workspace()
		if err != nil {
			return err
		}
		src, err := ws.Source(args[1])
		if err != nil {
			return err
		}
		fmt.Println("hashing and copying into the library (large files take a while)...")
		entry, err := lib.Import(src, args[2])
		if err != nil {
			return err
		}
		fmt.Printf("%s in library: %s (%d MiB, sha256 %s)\n", entry.ID, entry.Filename, entry.Size>>20, entry.SHA256)
		if src.SHA256 == "" {
			fmt.Printf("PIN IT: add `sha256: %s` to the manifest\n", entry.SHA256)
		}
		return nil
	default:
		return fmt.Errorf("sources: unknown subcommand %q", args[0])
	}
}

func cmdRecipes(env *Env, args []string) error {
	ws, err := env.workspace()
	if err != nil {
		return err
	}
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "list":
		rs, err := ws.Recipes()
		if err != nil {
			return err
		}
		if len(rs) == 0 {
			fmt.Println("no recipes in", filepath.Join(ws.Dir, "recipes"))
			return nil
		}
		for _, r := range rs {
			fmt.Printf("  %-24s %-10s %s\n", r.ID, r.OS.Type, r.Name)
		}
		return nil
	case "lint":
		findings, err := ws.LintAll(0)
		if err != nil {
			return err
		}
		if len(findings) == 0 {
			fmt.Println("clean")
			return nil
		}
		bad := false
		for id, fs := range findings {
			for _, f := range fs {
				fmt.Printf("  %s: %s\n", id, f)
				if f.Severity == "error" {
					bad = true
				}
			}
		}
		if bad {
			return fmt.Errorf("lint errors")
		}
		return nil
	default:
		return fmt.Errorf("recipes: unknown subcommand %q", sub)
	}
}

func cmdBuild(ctx context.Context, env *Env, args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	rebuild := fs.Bool("rebuild", false, "ignore the artifact cache")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("build <recipe-id> [--rebuild]")
	}
	ws, err := env.workspace()
	if err != nil {
		return err
	}
	lib, err := env.library()
	if err != nil {
		return err
	}
	art, err := buildArtifact(ctx, env, ws, lib, fs.Arg(0), *rebuild)
	if err != nil {
		return err
	}
	fmt.Printf("artifact: %s\n", art.Path)
	fmt.Printf("  kind=%s size=%d MiB sha256=%s\n", art.Kind, art.Size>>20, art.SHA256)
	// The check only exists for Windows media, and only the person holding the
	// imaged machine can run it — so it is worth naming here rather than
	// leaving them to find a file they did not know was written.
	if r, rerr := ws.Recipe(fs.Arg(0)); rerr == nil && r.Windows != nil {
		fmt.Println("\nAfter imaging a machine, check it matches this build:")
		fmt.Println(`  powershell -ExecutionPolicy Bypass -File C:\Windows\Setup\Scripts\verify.ps1`)
	}
	return nil
}

// buildArtifact lints (errors block), then composes.
func buildArtifact(ctx context.Context, env *Env, ws *workspace.Workspace, lib *library.Library, recipeID string, rebuild bool) (*compose.Artifact, error) {
	r, err := ws.Recipe(recipeID)
	if err != nil {
		return nil, err
	}
	hasError := false
	for _, f := range r.Lint() {
		fmt.Printf("  lint %s\n", f)
		if f.Severity == "error" {
			hasError = true
		}
	}
	if hasError {
		return nil, fmt.Errorf("lint errors block building — fix the recipe")
	}
	prog := &stageProgress{}
	defer prog.finish()
	return compose.Build(ctx, compose.Request{
		Workspace: ws, Library: lib, Recipe: r,
		CLIVars: env.Vars, Rebuild: rebuild,
		Progress: prog.report,
	})
}

func cmdGC(env *Env) error {
	lib, err := env.library()
	if err != nil {
		return err
	}
	freed, err := lib.GC()
	if err != nil {
		return err
	}
	fmt.Printf("freed %d MiB\n", freed>>20)
	return nil
}
