// Package cli implements the composer command line.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/DustanBaker/the-composer/internal/buildinfo"
	"github.com/DustanBaker/the-composer/internal/library"
	"github.com/DustanBaker/the-composer/internal/workspace"
)

const usage = `The Composer — build bootable installation USB media from recipes.

Usage: composer <command> [args]

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
  drivers inspect <pack>    what a dir/.zip/.cab/.inf covers (class, versions, hardware IDs)
  drivers add --id <n> <pack>  stage a pack (workspace or library+manifest) + recipe snippet
  drivers scan              list this machine's devices that still need drivers (Windows)

Building and flashing
  build <recipe>            compose a bootable artifact into the library
  devices                   list candidate USB targets
  flash <recipe|.img> <device>  write and verify a stick (asks for typed size confirm)
  capture <device>          read a working stick into the library as a master image

Other
  serve [--port 8931]       local web UI (recipes, devices, build, flash, live progress)
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
	if len(rest) == 0 {
		fmt.Print(usage)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	cmd, cmdArgs := rest[0], rest[1:]
	var err error
	switch cmd {
	case "version", "--version":
		fmt.Println("composer", buildinfo.Version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	case "init":
		err = cmdInit(cmdArgs)
	case "doctor":
		err = cmdDoctor(ctx, env)
	case "devices":
		err = cmdDevices(ctx)
	case "sources":
		err = cmdSources(ctx, env, cmdArgs)
	case "recipes":
		err = cmdRecipes(env, cmdArgs)
	case "drivers":
		err = cmdDrivers(ctx, env, cmdArgs)
	case "build":
		err = cmdBuild(ctx, env, cmdArgs)
	case "flash":
		err = cmdFlash(ctx, env, cmdArgs)
	case "capture":
		err = cmdCapture(ctx, env, cmdArgs)
	case "flash-worker":
		return cmdFlashWorker(ctx, cmdArgs)
	case "gc":
		err = cmdGC(env)
	case "serve":
		err = cmdServe(ctx, env, cmdArgs)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
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
