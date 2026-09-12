package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"path/filepath"

	"github.com/DustanBaker/uplink-composer/internal/appcatalog"
	"github.com/DustanBaker/uplink-composer/internal/driverresolve"
	"github.com/DustanBaker/uplink-composer/internal/hwdetect"
	"github.com/DustanBaker/uplink-composer/internal/library"
	"github.com/DustanBaker/uplink-composer/internal/oscatalog"
	"github.com/DustanBaker/uplink-composer/internal/recipe"
)

func cmdCatalog(ctx context.Context, env *Env, _ []string) error {
	refreshCatalog(ctx, env)
	fmt.Printf("Operating systems (uplink install <id>) — list: %s\n", oscatalog.Source())
	headings := map[oscatalog.Category]string{
		oscatalog.Desktop:   "Desktop",
		oscatalog.Server:    "Server",
		oscatalog.Appliance: "Single-board and appliance",
	}
	for _, cat := range []oscatalog.Category{oscatalog.Desktop, oscatalog.Server, oscatalog.Appliance} {
		first := true
		for _, e := range oscatalog.Catalog() {
			if e.Group() != cat {
				continue
			}
			if first {
				fmt.Printf("\n  %s\n", headings[cat])
				first = false
			}
			tags := string(e.Family)
			if a := e.CPUArch(); a != "amd64" {
				tags += "/" + a
			}
			fmt.Printf("    %-24s %-13s %s\n", e.ID, tags, e.Name)
			detail := fmt.Sprintf("%s — %s", e.Version, e.Notes)
			if len(e.Editions) > 0 {
				detail += "  editions: " + strings.Join(e.Editions, ", ")
			}
			fmt.Printf("      %s\n", detail)
			// On its own line rather than in the tag column, which is too
			// narrow for it and would push every other name out of alignment.
			if e.ImportOnly() && e.ImportFrom != "" {
				fmt.Printf("      bring your own ISO, from %s\n", e.ImportFrom)
			}
		}
	}
	return nil
}

// cmdApps lists the programs Quick Install can add, grouped by category.
func cmdApps(_ *Env, _ []string) error {
	fmt.Println("Programs `uplink install --apps` can add (comma-separated ids):")
	for _, cat := range appcatalog.Categories() {
		fmt.Printf("\n  %s\n", cat)
		for _, a := range appcatalog.Catalog() {
			if a.Category != cat {
				continue
			}
			where := ""
			if a.Winget == "" {
				where = "  (no Windows package)"
			}
			fmt.Printf("    %-18s %s%s\n", a.ID, a.Name, where)
		}
	}
	fmt.Println("\nExample:")
	fmt.Println("  uplink install windows-11 --drivers --apps chrome,7zip,vlc")
	fmt.Println("\nThese install at first boot with winget, so the machine needs to be")
	fmt.Println("online then — staging its network driver (--drivers) helps.")
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
	var driversFor stringList
	fs.Var(&driversFor, "drivers-for", "Windows: stage drivers for another machine, e.g.\n"+
		"\"dell:OptiPlex 7010\" (repeatable; one stick can carry several models)")
	apps := fs.String("apps", "", "Windows: programs to install at first boot (see `uplink apps`)")
	iso := fs.String("iso", "", "use an ISO you downloaded instead of fetching it")
	yes := fs.Bool("yes", false, "skip the typed size confirmation")
	buildOnly := fs.Bool("build-only", false, "stop after building; do not flash")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		return fmt.Errorf("install <os-id> [device] [--edition …] [--account local|oobe] [--debloat …]")
	}
	refreshCatalog(ctx, env)
	e, ok := oscatalog.Get(fs.Arg(0))
	if !ok {
		return fmt.Errorf("unknown OS %q — see `uplink catalog`", fs.Arg(0))
	}
	lib, err := env.library()
	if err != nil {
		return err
	}
	// Said now rather than after the device has been chosen and armed: there
	// is no download to attempt, so nothing later can rescue this.
	if e.ImportOnly() && *iso == "" && !oscatalog.InLibrary(lib, e) {
		return e.ImportOnlyError()
	}

	devArg := ""
	if fs.NArg() == 2 {
		devArg = fs.Arg(1)
	}
	var dev, derr = pickDevice(ctx, devArg)
	if !*buildOnly && derr != nil {
		return derr
	}

	if *iso != "" {
		if err := importISO(lib, e, *iso); err != nil {
			return err
		}
	}

	// Detected hardware and named models are additive: pnputil installs only
	// what matches the machine being imaged, so one stick can carry packs for
	// several models.
	var hw []recipe.HardwareSpec
	if *withDrivers {
		var err error
		if hw, err = detectedHardware(ctx, e); err != nil {
			return err
		}
	}
	for _, spec := range driversFor {
		h, err := hardwareForModel(spec, e)
		if err != nil {
			return err
		}
		hw = append(hw, h)
	}

	var appIDs []string
	if strings.TrimSpace(*apps) != "" {
		appIDs = strings.Split(*apps, ",")
		pkgs, err := appcatalog.WingetIDs(appIDs)
		if err != nil {
			return err
		}
		fmt.Printf("Will install %d program(s) at first boot: %s\n", len(pkgs), strings.Join(pkgs, ", "))
	}

	fmt.Printf("Building %s installer media...\n", e.Name)
	prog := &stageProgress{}
	art, err := oscatalog.BuildQuick(ctx, lib, e, oscatalog.Options{
		Edition: *edition, AccountMode: *account, Debloat: *debloat, BypassRequirement: *bypass,
		Hardware: hw, Apps: appIDs,
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

// stringList collects a repeatable string flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ", ") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// hardwareForModel parses a --drivers-for target into a hardware entry.
func hardwareForModel(spec string, e oscatalog.Entry) (recipe.HardwareSpec, error) {
	if e.Family != oscatalog.Windows {
		return recipe.HardwareSpec{}, fmt.Errorf("--drivers-for is a Windows option — Linux ships its drivers in the kernel")
	}
	h, err := driverresolve.SpecForModel(spec, e.DriverOS())
	if err != nil {
		return recipe.HardwareSpec{}, fmt.Errorf("--drivers-for %v", err)
	}
	return h, nil
}

// importISO files a downloaded ISO under the catalog entry's id. Hashing and
// copying several gigabytes takes a while, so say so, and skip the work when
// the image is already there.
func importISO(lib *library.Library, e oscatalog.Entry, path string) error {
	if oscatalog.InLibrary(lib, e) {
		fmt.Printf("%s is already in the library — using that, ignoring --iso.\n", e.ID)
		fmt.Println("  (to replace it: uplink gc, or delete the blob the catalog names)")
		return nil
	}
	if err := oscatalog.CheckISO(path); err != nil {
		return err
	}
	fmt.Printf("Importing %s (hashing and copying several GB, this takes a minute)...\n", filepath.Base(path))
	entry, err := oscatalog.ImportISO(lib, e, path)
	if err != nil {
		return err
	}
	fmt.Printf("  in library as %s: %d MiB, sha256 %s\n", entry.ID, entry.Size>>20, entry.SHA256)
	return nil
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
