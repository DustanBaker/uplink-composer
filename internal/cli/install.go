package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/DustanBaker/uplink-composer/internal/driverresolve"
	"github.com/DustanBaker/uplink-composer/internal/hwdetect"
	"github.com/DustanBaker/uplink-composer/internal/oscatalog"
	"github.com/DustanBaker/uplink-composer/internal/recipe"
)

func cmdCatalog(_ *Env, _ []string) error {
	fmt.Println("Built-in operating systems (uplink install <id>):")
	for _, e := range oscatalog.Catalog() {
		opts := ""
		if len(e.Editions) > 0 {
			opts = "  editions: " + strings.Join(e.Editions, ", ")
		}
		fmt.Printf("  %-26s %-8s %s%s\n", e.ID, e.Family, e.Name, "")
		fmt.Printf("      %s (%s)%s\n", e.Version, e.Notes, opts)
	}
	return nil
}

// cmdInstall is Quick Install from the CLI: pick a catalog OS, build media
// with a few options, and flash the attached stick.
func cmdInstall(ctx context.Context, env *Env, args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	edition := fs.String("edition", "", "Windows edition (Pro, Home, …)")
	account := fs.String("account", "local", "Windows account setup: local | oobe")
	debloat := fs.String("debloat", "standard", "Windows debloat: off | standard | aggressive")
	bypass := fs.Bool("bypass-checks", false, "Windows: skip TPM/Secure Boot/RAM checks")
	withDrivers := fs.Bool("drivers", false, "Windows: detect this machine and stage its drivers")
	yes := fs.Bool("yes", false, "skip the typed size confirmation")
	buildOnly := fs.Bool("build-only", false, "stop after building; do not flash")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		return fmt.Errorf("install <os-id> [device] [--edition …] [--account local|oobe] [--debloat …]")
	}
	e, ok := oscatalog.Get(fs.Arg(0))
	if !ok {
		return fmt.Errorf("unknown OS %q — see `uplink catalog`", fs.Arg(0))
	}
	lib, err := env.library()
	if err != nil {
		return err
	}

	devArg := ""
	if fs.NArg() == 2 {
		devArg = fs.Arg(1)
	}
	var dev, derr = pickDevice(ctx, devArg)
	if !*buildOnly && derr != nil {
		return derr
	}

	var hw []recipe.HardwareSpec
	if *withDrivers {
		var err error
		if hw, err = detectedHardware(ctx, e); err != nil {
			return err
		}
	}

	fmt.Printf("Building %s installer media...\n", e.Name)
	prog := &stageProgress{}
	art, err := oscatalog.BuildQuick(ctx, lib, e, oscatalog.Options{
		Edition: *edition, AccountMode: *account, Debloat: *debloat, BypassRequirement: *bypass,
		Hardware: hw,
	}, prog.report)
	prog.finish()
	if err != nil {
		return err
	}
	if *buildOnly {
		fmt.Printf("built: %s (%d MiB)\n", art.Path, art.Size>>20)
		return nil
	}
	return armAndFlash(ctx, art, dev, *yes)
}

// detectedHardware profiles this machine into the hardware entries Quick
// Install should hunt driver packs for. Whatever the catalogs turn out not to
// carry is dropped during the build, not here.
func detectedHardware(ctx context.Context, e oscatalog.Entry) ([]recipe.HardwareSpec, error) {
	if e.Family != oscatalog.Windows {
		return nil, fmt.Errorf("--drivers is a Windows option — Linux ships its drivers in the kernel")
	}
	h, err := hwdetect.Detect(ctx)
	if err != nil {
		return nil, err
	}
	fmt.Printf("Detected %s %s (%s)\n", h.Vendor, h.Model, h.CPU)
	ids := h.DriverHWIDs()
	if v := h.KnownVendor(); v != "" {
		fmt.Printf("  %s model driver pack, plus %d GPU/network hardware ID(s)\n", v, len(ids))
	} else {
		fmt.Printf("  no per-model feed for this maker; %d GPU/network hardware ID(s) to look up\n", len(ids))
	}
	hw := driverresolve.SpecsFor(h, e.DriverOS())
	if len(hw) == 0 {
		return nil, fmt.Errorf("nothing to resolve: this machine has no Dell/Lenovo/HP model feed and no PCI GPU or network device was detected")
	}
	return hw, nil
}
