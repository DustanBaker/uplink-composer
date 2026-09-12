// Package cli implements the uplink command line.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/DustanBaker/uplink-composer/internal/buildinfo"
	"github.com/DustanBaker/uplink-composer/internal/library"
	"github.com/DustanBaker/uplink-composer/internal/selfupdate"
	"github.com/DustanBaker/uplink-composer/internal/workspace"
)

const usage = `The Uplink CompOSer — build bootable installation USB media from recipes.

Usage: uplink <recipe>            the whole thing: pull sources, build, flash the attached stick, verify
       uplink                     same, in a workspace with a single recipe
       uplink <command> [args]

One-shot
  go <recipe|.img> [device]  pull missing sources, build, flash, verify (--yes, --build-only)

Quick install (no workspace needed)
  catalog                   list the built-in operating systems
  install <os-id> [device]  build + flash an OS from the catalog
                            (--edition, --account local|oobe, --debloat, --bypass-checks,
                             --drivers to detect this machine and stage its drivers,
                             --drivers-for "dell:OptiPlex 7010" for another model (repeatable),
                             --apps chrome,7zip,... to install programs at first boot,
                             --iso <file> to use an ISO you downloaded yourself)
  detect                    what this computer is, and the drivers it needs
  apps                      programs --apps can install

Workspace
  init --org <name> [dir]   scaffold a new org workspace
  recipes list              recipes in the workspace
  recipes lint              production-lesson checks + repo hygiene

Library (machine-local, multi-GB safe)
  sources list              manifests and their library status
  sources pull <id>         download a pinned source (--pin-tofu to trust-on-first-use;
                            provider: fido manifests fetch official Windows ISOs directly)
  sources import <id> <file>  add a manually-downloaded file (e.g. Windows ISO)
  gc                        drop unreferenced blobs and tmp files

Drivers
  drivers search <dell|lenovo|hp> "<model>"   find the vendor's driver pack for a model (--add)
  drivers search mscatalog "<hardware-id>"     find a driver in the Microsoft Update Catalog (--add)
  drivers resolve <recipe>  fetch packs for every windows.hardware entry (compose does this itself)
  drivers inspect <pack>    what a dir/.zip/.cab/.inf covers (class, versions, hardware IDs)
  drivers add --id <n> <pack>  stage a pack you already have + recipe snippet
  drivers scan              list this machine's devices that still need drivers (Windows)

Building and flashing
  build <recipe>            compose a bootable artifact into the library
  devices                   list candidate USB targets
  flash <recipe|.img> <device> [<device> …]   write and verify sticks, all at once
                            (--all for every attached stick; one elevation for the batch)
  clone <device>            read a working stick into the library as a master image
                            (--to <device> … , or --to all, to write it straight to blanks)

Pick an interface
  tui                       full-screen terminal wizard (pick OS, options, stick)
  serve [--port 8931]       local web UI (recipes, devices, build, flash, live progress)

Other
  update [--check]          replace this binary with the newest release
  uninstall [--purge]       remove the installed program (--purge also deletes
                            the downloaded-image library; workspaces are never touched)
  doctor                    check this host's tooling and configuration
  version                   print version

Global flags (before or after the command):
  -w <dir>      workspace directory (default: current directory, searching upward)
  --var k=v     set/override a template var (repeatable)
  --library <dir>  override the library root
`

// Env carries resolved global state into commands.
type Env struct {
	WorkspaceDir string
	LibraryRoot  string
	Vars         map[string]string
}

func (e *Env) library() (*library.Library, error) {
	root := e.LibraryRoot
	if root == "" {
		root = library.DefaultRoot()
	}
	return library.Open(root)
}

func (e *Env) workspace() (*workspace.Workspace, error) {
	dir, err := workspace.Find(e.WorkspaceDir)
	if err != nil {
		return nil, err
	}
	return workspace.Load(dir)
}

// Main is the process entry point; returns the exit code.
func Main(args []string) int {
	env := &Env{WorkspaceDir: ".", Vars: map[string]string{}}

	// Extract global flags anywhere on the line; leave the rest.
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-w" || a == "--workspace":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: -w needs a directory")
				return 2
			}
			i++
			env.WorkspaceDir = args[i]
		case a == "--library":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --library needs a directory")
				return 2
			}
			i++
			env.LibraryRoot = args[i]
		case a == "--var":
			if i+1 >= len(args) || !strings.Contains(args[i+1], "=") {
				fmt.Fprintln(os.Stderr, "error: --var needs k=v")
				return 2
			}
			i++
			kv := strings.SplitN(args[i], "=", 2)
			env.Vars[kv[0]] = kv[1]
		default:
			rest = append(rest, a)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Windows cannot delete the previous binary while it is still running, so
	// an update leaves it behind for the next run to clear.
	selfupdate.CleanupOld()

	// "compose this": no arguments in a one-recipe workspace runs it.
	if len(rest) == 0 {
		if id := soleRecipe(env); id != "" {
			rest = []string{id}
		} else {
			fmt.Print(usage)
			return 2
		}
	}

	cmd, cmdArgs := rest[0], rest[1:]
	var err error
	switch cmd {
	case "version", "--version":
		fmt.Println("uplink", buildinfo.Version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	case "go":
		err = cmdGo(ctx, env, cmdArgs)
	case "init":
		err = cmdInit(cmdArgs)
	case "doctor":
		err = cmdDoctor(ctx, env)
	case "update":
		err = cmdUpdate(ctx, env, cmdArgs)
	case "uninstall":
		err = cmdUninstall(ctx, env, cmdArgs)
	case "devices":
		err = cmdDevices(ctx)
	case "sources":
		err = cmdSources(ctx, env, cmdArgs)
	case "recipes":
		err = cmdRecipes(env, cmdArgs)
	case "catalog":
		err = cmdCatalog(env, cmdArgs)
	case "apps":
		err = cmdApps(env, cmdArgs)
	case "install":
		err = cmdInstall(ctx, env, cmdArgs)
	case "detect":
		err = cmdDetect(ctx, env, cmdArgs)
	case "drivers":
		err = cmdDrivers(ctx, env, cmdArgs)
	case "build":
		err = cmdBuild(ctx, env, cmdArgs)
	case "flash":
		err = cmdFlash(ctx, env, cmdArgs)
	case "clone", "capture": // capture was the old name for this
		err = cmdClone(ctx, env, cmdArgs)
	case "flash-worker":
		return cmdFlashWorker(ctx, cmdArgs)
	case "gc":
		err = cmdGC(env)
	case "serve":
		err = cmdServe(ctx, env, cmdArgs)
	case "tui":
		err = cmdTUI(ctx, env, cmdArgs)
	default:
		// A recipe id (or artifact path) as the first word is the one-shot
		// pipeline: `compose nuc-win11`.
		if isRecipeOrArtifact(env, cmd) {
			err = cmdGo(ctx, env, rest)
			break
		}
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

// soleRecipe returns the id of the workspace's only recipe, or "".
func soleRecipe(env *Env) string {
	ws, err := env.workspace()
	if err != nil {
		return ""
	}
	rs, err := ws.Recipes()
	if err != nil || len(rs) != 1 {
		return ""
	}
	return rs[0].ID
}

func isRecipeOrArtifact(env *Env, word string) bool {
	if strings.HasSuffix(strings.ToLower(word), ".img") {
		return true
	}
	ws, err := env.workspace()
	if err != nil {
		return false
	}
	_, err = ws.Recipe(word)
	return err == nil
}

// stageProgress renders coarse progress on one console line.
type stageProgress struct {
	lastStage string
	lastPct   int
}

func (p *stageProgress) report(stage string, done, total int64) {
	if stage != p.lastStage {
		if p.lastStage != "" {
			fmt.Println()
		}
		fmt.Printf("%s...", stage)
		p.lastStage, p.lastPct = stage, -1
	}
	if total > 0 {
		pct := int(done * 100 / total)
		if pct/5 > p.lastPct/5 {
			fmt.Printf(" %d%%", pct)
			p.lastPct = pct
		}
	}
}

func (p *stageProgress) finish() {
	if p.lastStage != "" {
		fmt.Println()
	}
}
