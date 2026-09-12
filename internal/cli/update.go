package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/DustanBaker/uplink-composer/internal/buildinfo"
	"github.com/DustanBaker/uplink-composer/internal/selfupdate"
)

// cmdUpdate replaces this binary with the newest published release.
func cmdUpdate(ctx context.Context, _ *Env, args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	check := fs.Bool("check", false, "report whether a newer version exists; change nothing")
	force := fs.Bool("force", false, "install the published release even if it is not newer")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	fmt.Printf("Running %s; checking for updates...\n", buildinfo.Version)
	rel, err := selfupdate.Check(ctx)
	if err != nil {
		return err
	}
	if !rel.Newer && !*force {
		fmt.Printf("%s is the newest release — nothing to do.\n", rel.Version)
		return nil
	}
	if rel.Newer {
		fmt.Printf("\n%s is available (this is %s).\n", rel.Version, buildinfo.Version)
	} else {
		fmt.Printf("\nReinstalling %s.\n", rel.Version)
	}
	if notes := firstLines(rel.Notes, 6); notes != "" {
		fmt.Printf("\n%s\n", notes)
	}
	if *check {
		fmt.Println("\nInstall it with: uplink update")
		return nil
	}

	prog := &stageProgress{}
	path, err := selfupdate.Apply(ctx, rel, func(done, total int64) {
		prog.report("downloading "+rel.Asset, done, total)
	})
	prog.finish()
	if err != nil {
		return err
	}
	fmt.Printf("Updated %s to %s.\n", path, rel.Version)
	fmt.Println("Already-running copies (the app window, a serve session) keep the old build until restarted.")
	return nil
}

// firstLines trims release notes down to a preview.
func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = append(lines[:n], "  …")
	}
	for i, l := range lines {
		lines[i] = "  " + strings.TrimRight(l, "\r")
	}
	return strings.Join(lines, "\n")
}
