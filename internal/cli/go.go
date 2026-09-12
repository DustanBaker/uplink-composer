package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DustanBaker/the-composer/internal/compose"
	"github.com/DustanBaker/the-composer/internal/device"
	"github.com/DustanBaker/the-composer/internal/elevate"
	"github.com/DustanBaker/the-composer/internal/flashrun"
	"github.com/DustanBaker/the-composer/internal/helpers"
	"github.com/DustanBaker/the-composer/internal/library"
	"github.com/DustanBaker/the-composer/internal/manifest"
	"github.com/DustanBaker/the-composer/internal/recipe"
	"github.com/DustanBaker/the-composer/internal/workspace"
)

// cmdGo is the one-shot pipeline — "compose this": pull whatever pinned
// sources are missing, build, pick the attached USB stick, arm, flash,
// verify. `composer <recipe>` and a bare `composer` in a one-recipe
// workspace both land here.
func cmdGo(ctx context.Context, env *Env, args []string) error {
	fs := flag.NewFlagSet("go", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "skip the typed size confirmation (scripted use)")
	rebuild := fs.Bool("rebuild", false, "ignore the artifact cache")
	tofu := fs.Bool("pin-tofu", false, "accept unpinned sources on first download (prints the hash to pin)")
	buildOnly := fs.Bool("build-only", false, "stop after building; do not flash")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		return fmt.Errorf("go <recipe-id|artifact.img> [device] [--yes] [--build-only]")
	}
	what := fs.Arg(0)
	devArg := ""
	if fs.NArg() == 2 {
		devArg = fs.Arg(1)
	}

	var dev device.Device
	if !*buildOnly {
		d, err := pickDevice(ctx, devArg)
		if err != nil {
			return err
		}
		dev = d
	}

	var art *compose.Artifact
	if strings.HasSuffix(strings.ToLower(what), ".img") {
		a, err := compose.LoadArtifact(compose.MetaPath(what))
		if err != nil {
			return fmt.Errorf("no artifact metadata next to %s: %w", what, err)
		}
		art = a
	} else {
		ws, err := env.workspace()
		if err != nil {
			return err
		}
		lib, err := env.library()
		if err != nil {
			return err
		}
		r, err := ws.Recipe(what)
		if err != nil {
			return err
		}
		if err := resolveHardware(ctx, ws, lib, r); err != nil {
			return err
		}
		if err := ensureSources(ctx, ws, lib, r, *tofu); err != nil {
			return err
		}
		art, err = buildArtifact(ctx, env, ws, lib, what, *rebuild)
		if err != nil {
			return err
		}
	}
	if *buildOnly {
		fmt.Printf("built: %s (%d MiB)\n", art.Path, art.Size>>20)
		return nil
	}
	return armAndFlash(ctx, art, dev, *yes)
}

// pickDevice resolves an explicit device argument, or — the common case —
// the single flashable USB stick attached. Anything ambiguous stops.
func pickDevice(ctx context.Context, arg string) (device.Device, error) {
	devs, err := device.List(ctx)
	if err != nil {
		return device.Device{}, err
	}
	if arg != "" {
		dev, err := matchDevice(devs, arg)
		if err != nil {
			return device.Device{}, err
		}
		if !dev.Flashable() {
			return device.Device{}, fmt.Errorf("%s is not flashable (bus=%s, system=%v) — `composer devices` shows valid targets", dev.ID, dev.Bus, dev.System)
		}
		return dev, nil
	}
	var usable []device.Device
	for _, d := range devs {
		if d.Flashable() {
			usable = append(usable, d)
		}
	}
	switch len(usable) {
	case 1:
		return usable[0], nil
	case 0:
		return device.Device{}, fmt.Errorf("no USB stick attached — plug one in and re-run (`composer devices` lists targets)")
	default:
		var ids []string
		for _, d := range usable {
			ids = append(ids, d.String())
		}
		return device.Device{}, fmt.Errorf("%d USB sticks attached; name the target:\n  %s", len(usable), strings.Join(ids, "\n  "))
	}
}

// ensureSources pulls every pinned source the recipe needs that is not in
// the library yet. Windows tree-mode builds skip the ISO when the master
// tree is present on this machine.
func ensureSources(ctx context.Context, ws *workspace.Workspace, lib *library.Library, r *recipe.Recipe, tofu bool) error {
	skipOS := false
	if r.OS.Type == recipe.OSWindows && r.OS.TreePath != "" {
		tp := r.OS.TreePath
		if !filepath.IsAbs(tp) {
			tp = filepath.Join(ws.Dir, filepath.FromSlash(tp))
		}
		if st, err := os.Stat(tp); err == nil && st.IsDir() {
			skipOS = true
		}
	}
	for _, ref := range r.SourceRefs() {
		if skipOS && ref == r.OS.Source {
			continue
		}
		if _, err := lib.Resolve(ref); err == nil {
			continue
		}
		src, err := ws.Source(ref)
		if err != nil {
			return fmt.Errorf("%s is not in the library and has no manifest — add one, or `composer sources import %s <file>`", ref, ref)
		}
		if src.URL == "" && src.Provider == "" {
			return fmt.Errorf("%s is not in the library and its manifest has no url/provider — `composer sources import %s <file>`", ref, ref)
		}
		fmt.Printf("pulling %s...\n", ref)
		prog := &stageProgress{}
		resolver := func(ctx context.Context, s *manifest.Source) (string, error) {
			prog.report("resolving download URL", 0, -1)
			return helpers.ResolveFidoURL(ctx, lib.HelpersDir(), s.Fido)
		}
		entry, err := lib.Pull(ctx, src, tofu, resolver, func(done, total int64) {
			prog.report("download", done, total)
		})
		prog.finish()
		var unpinned *library.ErrUnpinned
		if errors.As(err, &unpinned) {
			return fmt.Errorf("%s downloaded but is unpinned (sha256 %s) — add that sha256 to its manifest, or re-run with --pin-tofu", ref, unpinned.SHA256)
		}
		if err != nil {
			return err
		}
		fmt.Printf("  %s in library (%d MiB)\n", entry.ID, entry.Size>>20)
		if src.SHA256 == "" {
			fmt.Printf("  PIN IT: add `sha256: %s` to manifests/%s.yaml\n", entry.SHA256, ref)
		}
	}
	return nil
}

// armAndFlash shows the target, takes the typed-size confirmation, and
// runs the (elevated) flash with progress.
func armAndFlash(ctx context.Context, art *compose.Artifact, dev device.Device, yes bool) error {
	fmt.Println()
	fmt.Println("About to WIPE this device:")
	fmt.Println(" ", dev.String())
	if len(dev.Mounts) > 0 {
		fmt.Println("  currently mounted at:", strings.Join(dev.Mounts, ", "))
	}
	fmt.Printf("  writing: %s (%d MiB, verify %s)\n", filepath.Base(art.Path), art.Size>>20, art.Verify)
	if err := confirmSize(dev, yes); err != nil {
		return err
	}
	if !elevate.IsElevated() {
		fmt.Println("elevating flash worker —", elevate.Hint())
	}
	prog := &stageProgress{}
	err := flashrun.RunFlash(ctx, art, dev, prog.report)
	prog.finish()
	if err != nil {
		return err
	}
	fmt.Printf("Done. %s is written and verified — safe to remove.\n", dev.ID)
	return nil
}
