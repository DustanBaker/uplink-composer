package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/uplinkresearch/bootwright/internal/device"
	"github.com/uplinkresearch/bootwright/internal/diskutil"
	"github.com/uplinkresearch/bootwright/internal/elevate"
	"github.com/uplinkresearch/bootwright/internal/flashrun"
)

func cmdDisks(ctx context.Context, env *Env, args []string) error {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "list":
		return disksList(ctx)
	case "inspect":
		return disksInspect(ctx, args)
	case "prepare":
		return disksPrepare(ctx, args)
	default:
		return fmt.Errorf("disks: unknown subcommand %q (list, inspect, prepare)", sub)
	}
}

// disksList shows every attached disk with what is actually on it.
func disksList(ctx context.Context) error {
	devs, err := device.List(ctx)
	if err != nil {
		return err
	}
	if len(devs) == 0 {
		fmt.Println("no disks found")
		return nil
	}
	for _, d := range devs {
		fmt.Println(" ", d.String())
		if !d.Flashable() {
			continue // only removable media is inspectable here
		}
		l, err := diskutil.Inspect(ctx, d)
		if err != nil {
			fmt.Printf("      (could not read the partition table: %v)\n", err)
			continue
		}
		printLayout(l, "      ")
	}
	fmt.Println("\nFix a stick that will not mount or shows the wrong size:")
	fmt.Println("  bootwright disks prepare <device> [--fs exfat|fat32|ntfs] [--label NAME] [--scheme gpt|mbr]")
	return nil
}

func disksInspect(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("disks inspect <device>")
	}
	dev, err := pickDevice(ctx, args[0])
	if err != nil {
		return err
	}
	l, err := diskutil.Inspect(ctx, dev)
	if err != nil {
		return err
	}
	fmt.Println(dev.String())
	printLayout(l, "  ")
	return nil
}

func printLayout(l *diskutil.Layout, indent string) {
	fmt.Printf("%spartition table: %s\n", indent, l.Scheme)
	for _, p := range l.Parts {
		mounts := ""
		if len(p.Mounts) > 0 {
			mounts = "  mounted " + strings.Join(p.Mounts, ", ")
		}
		label := ""
		if p.Label != "" {
			label = fmt.Sprintf(" %q", p.Label)
		}
		fmt.Printf("%s  %d. %7.1f GB  %s%s%s\n", indent, p.Number,
			float64(p.Size)/1e9, p.Type, label, mounts)
	}
	if u := l.UnusedBytes(); u > 0 {
		fmt.Printf("%s  %7.1f GB unpartitioned\n", indent, float64(u)/1e9)
	}
	for _, n := range l.Notes {
		fmt.Printf("%s  note: %s\n", indent, n)
	}
}

func disksPrepare(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("disks prepare", flag.ContinueOnError)
	fsType := fs.String("fs", "exfat", "filesystem: exfat | fat32 | ntfs")
	scheme := fs.String("scheme", "gpt", "partition table: gpt | mbr")
	label := fs.String("label", "BOOTWRIGHT", "volume label")
	yes := fs.Bool("yes", false, "skip the typed size confirmation")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("disks prepare <device> [--fs exfat] [--scheme gpt] [--label NAME]")
	}
	dev, err := pickDevice(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	opts := diskutil.Options{
		Scheme: diskutil.Scheme(strings.ToLower(*scheme)),
		FS:     diskutil.FS(strings.ToLower(*fsType)),
		Label:  *label,
	}
	// Both gates before anything destructive: policy, then the options the
	// platform tooling would reject later anyway.
	if err := diskutil.Guard(dev); err != nil {
		return err
	}
	if err := opts.Validate(dev); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("About to ERASE this device and give it one new volume:")
	fmt.Println(" ", dev.String())
	if l, err := diskutil.Inspect(ctx, dev); err == nil {
		printLayout(l, "    ")
	}
	fmt.Printf("  new layout: %s, one partition, %s, labelled %q\n", opts.Scheme, opts.FS, opts.Label)
	if err := confirmSize(dev, *yes); err != nil {
		return err
	}
	if !elevate.IsElevated() {
		fmt.Println("elevating disk worker —", elevate.Hint())
	}
	prog := &stageProgress{}
	if _, err := flashrun.RunPrepare(ctx, dev, opts, prog.report); err != nil {
		prog.finish()
		return err
	}
	prog.finish()
	fmt.Printf("Done. %s has one %s volume labelled %q.\n", dev.ID, opts.FS, opts.Label)
	return nil
}
