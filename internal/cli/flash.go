package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DustanBaker/the-composer/internal/compose"
	"github.com/DustanBaker/the-composer/internal/device"
	"github.com/DustanBaker/the-composer/internal/elevate"
	"github.com/DustanBaker/the-composer/internal/flashrun"
)

func cmdFlash(ctx context.Context, env *Env, args []string) error {
	fs := flag.NewFlagSet("flash", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "skip the typed size confirmation (scripted use)")
	rebuild := fs.Bool("rebuild", false, "ignore the artifact cache")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("flash <recipe-id|artifact.img> <device> [--yes]")
	}
	what, devArg := fs.Arg(0), fs.Arg(1)

	// Resolve the device first — no point composing for a bad target.
	devs, err := device.List(ctx)
	if err != nil {
		return err
	}
	dev, err := matchDevice(devs, devArg)
	if err != nil {
		return err
	}
	if !dev.Flashable() {
		return fmt.Errorf("%s is not flashable (bus=%s, system=%v) — `composer devices` shows valid targets", dev.ID, dev.Bus, dev.System)
	}

	// Resolve the artifact.
	var art *compose.Artifact
	if strings.HasSuffix(strings.ToLower(what), ".img") {
		art, err = compose.LoadArtifact(compose.MetaPath(what))
		if err != nil {
			return fmt.Errorf("no artifact metadata next to %s (build it with `composer build`): %w", what, err)
		}
	} else {
		ws, err := env.workspace()
		if err != nil {
			return err
		}
		lib, err := env.library()
		if err != nil {
			return err
		}
		art, err = buildArtifact(ctx, env, ws, lib, what, *rebuild)
		if err != nil {
			return err
		}
	}

	// Arm: show exactly what will be destroyed and require the typed size.
	fmt.Println()
	fmt.Println("About to WIPE this device:")
	fmt.Println(" ", dev.String())
	if len(dev.Mounts) > 0 {
		fmt.Println("  currently mounted at:", strings.Join(dev.Mounts, ", "))
	}
	fmt.Printf("  writing: %s (%d MiB, verify %s)\n", filepath.Base(art.Path), art.Size>>20, art.Verify)
	if err := confirmSize(dev, *yes); err != nil {
		return err
	}

	if !elevate.IsElevated() {
		fmt.Println("elevating flash worker —", elevate.Hint())
	}
	prog := &stageProgress{}
	err = flashrun.RunFlash(ctx, art, dev, prog.report)
	prog.finish()
	if err != nil {
		return err
	}
	fmt.Printf("Done. %s is written and verified — safe to remove.\n", dev.ID)
	return nil
}

func cmdCapture(ctx context.Context, env *Env, args []string) error {
	fs := flag.NewFlagSet("capture", flag.ContinueOnError)
	out := fs.String("out", "", "output image path (default: library artifacts dir)")
	yes := fs.Bool("yes", false, "skip the typed size confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("capture <device> [--out file.img]")
	}
	devs, err := device.List(ctx)
	if err != nil {
		return err
	}
	dev, err := matchDevice(devs, fs.Arg(0))
	if err != nil {
		return err
	}
	if dev.System {
		return fmt.Errorf("refusing to capture the system disk")
	}
	outPath := *out
	if outPath == "" {
		lib, err := env.library()
		if err != nil {
			return err
		}
		name := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
				return r
			}
			return '-'
		}, dev.Model)
		outPath = filepath.Join(lib.ArtifactsDir(), fmt.Sprintf("capture-%s-%s.img", name, time.Now().Format("20060102-150405")))
	}

	fmt.Println("About to CAPTURE this device (read-only, through its last partition):")
	fmt.Println(" ", dev.String())
	fmt.Println("  to:", outPath)
	if err := confirmSize(dev, *yes); err != nil {
		return err
	}
	if !elevate.IsElevated() {
		fmt.Println("elevating capture worker —", elevate.Hint())
	}
	prog := &stageProgress{}
	result, err := flashrun.RunCapture(ctx, dev, outPath, prog.report)
	prog.finish()
	if err != nil {
		return err
	}
	fmt.Printf("Captured to %s (%s)\n", outPath, result)
	return nil
}

// confirmSize is the typed-size interlock ported from make-nuc-usb.sh.
func confirmSize(dev device.Device, skip bool) error {
	if skip {
		return nil
	}
	want := dev.SizeConfirmation()
	fmt.Printf("Type the device size (%s) to confirm: ", want)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return fmt.Errorf("confirmation aborted: %w", err)
	}
	if strings.TrimSpace(line) != want {
		return fmt.Errorf("confirmation mismatch — aborting, nothing written")
	}
	return nil
}

// cmdFlashWorker runs inside the elevated relaunch; exit code is the result.
func cmdFlashWorker(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("flash-worker", flag.ContinueOnError)
	jobPath := fs.String("job", "", "job file")
	progPath := fs.String("progress", "", "progress file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	return flashrun.Worker(ctx, *jobPath, *progPath)
}

// matchDevice resolves a user-typed device argument: the exact ID, or the
// platform shorthand (a disk number on Windows, sdX on Linux, diskN on mac).
func matchDevice(devs []device.Device, arg string) (device.Device, error) {
	norm := strings.ToLower(strings.TrimSpace(arg))
	for _, d := range devs {
		if strings.EqualFold(d.ID, arg) {
			return d, nil
		}
		short := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(d.ID, `\\.\`), "/dev/"))
		if short == norm || short == "r"+norm || strings.TrimPrefix(short, "physicaldrive") == norm {
			return d, nil
		}
	}
	var ids []string
	for _, d := range devs {
		if d.Flashable() {
			ids = append(ids, d.ID)
		}
	}
	if len(ids) == 0 {
		return device.Device{}, fmt.Errorf("no device matches %q and no flashable devices are attached", arg)
	}
	return device.Device{}, fmt.Errorf("no device matches %q — flashable: %s", arg, strings.Join(ids, ", "))
}
