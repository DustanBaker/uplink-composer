package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/DustanBaker/uplink-composer/internal/uninstall"
)

// cmdUninstall removes the installed program, and optionally the library of
// downloaded images with it.
func cmdUninstall(_ context.Context, _ *Env, args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	purge := fs.Bool("purge", false, "also delete the library of downloaded OS images and built media")
	dryRun := fs.Bool("dry-run", false, "list what would be removed and stop")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	plan, err := uninstall.Build()
	if err != nil {
		return err
	}
	if len(plan.Items) == 0 {
		fmt.Println("Nothing of The Uplink CompOSer found on this machine.")
		return nil
	}

	fmt.Println("This removes:")
	for _, it := range plan.Items {
		if it.Kind == uninstall.KindLibrary && !*purge {
			continue
		}
		size := ""
		if it.Bytes > 0 {
			size = fmt.Sprintf("  (%s)", human(it.Bytes))
		}
		fmt.Printf("  %-58s %s%s\n", it.Path, it.What, size)
	}

	if lib := plan.LibraryBytes(); lib > 0 && !*purge {
		fmt.Printf("\nKeeping the library (%s of downloaded OS images and built media).\n", human(lib))
		fmt.Println("  Add --purge to delete it too, or keep it and a reinstall starts with everything cached.")
	}
	// Said every time, because it is the thing someone would most fear
	// losing and the thing this command must never take.
	fmt.Println("\nYour workspaces are not touched — recipes, manifests and templates are your")
	fmt.Println("own git repositories and this does not know or care where they live.")

	if *dryRun {
		fmt.Println("\n--dry-run: nothing was removed.")
		return nil
	}
	if !*yes {
		prompt := "Remove it? Type uninstall to confirm: "
		if *purge {
			prompt = fmt.Sprintf("Remove it AND delete %s of downloaded images? Type uninstall to confirm: ", human(plan.LibraryBytes()))
		}
		fmt.Print("\n" + prompt)
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return fmt.Errorf("reading confirmation: %w", err)
		}
		if strings.TrimSpace(line) != "uninstall" {
			return fmt.Errorf("not confirmed — nothing was removed")
		}
	}

	removed, errs := uninstall.Run(plan, *purge)
	fmt.Printf("\nRemoved %d item(s).\n", len(removed))
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "  could not remove %v\n", e)
	}
	if uninstall.SelfIsDeferred {
		fmt.Println("The program file itself clears a few seconds after this exits.")
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d item(s) could not be removed", len(errs))
	}
	fmt.Println("Done. Open a new terminal for the PATH change to take effect.")
	return nil
}

// human renders a byte count the way someone reads a disk.
func human(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%d MB", n>>20)
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}
