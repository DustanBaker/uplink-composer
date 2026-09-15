package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Agent is one run on one machine.
type Agent struct {
	// Dir is where the agent, the manifest and the staged files live on the
	// imaged machine (C:\Windows\Setup\Scripts).
	Dir      string
	Manifest *Manifest
	J        *Journal
	State    *State
}

// Apply does everything the manifest asks for, in the recipe's order, and
// records what happened. It returns an error only when it could not start:
// a step that fails is logged and the rest still run, because a machine with
// drivers and no Spotify is worth more than a machine with neither.
func Apply(dir string) error {
	m, err := LoadManifest(filepath.Join(dir, ManifestName))
	if err != nil {
		return err
	}
	j, err := OpenJournal(dir)
	if err != nil {
		return err
	}
	defer j.Close()

	a := &Agent{Dir: dir, Manifest: m, J: j, State: LoadState(dir)}
	start := time.Now()
	vendor, model := a.machineModel()
	machine := strings.TrimSpace(vendor + " " + model)
	if machine == "" {
		machine = "unknown machine"
	}
	a.J.Info("", "first-boot agent starting for recipe %s on %s (%s)", m.Recipe, machine, runtime.GOOS)

	for _, step := range m.Steps {
		if a.State.Finished(step) {
			a.J.Info(step, "already done on an earlier boot, skipping")
			continue
		}
		switch step {
		case stepDrivers:
			a.driversStep()
		case stepDebloat:
			a.debloatStep()
		case stepApps:
			a.appsStep()
		default:
			a.J.Info(step, "no such step in this agent, skipping")
			continue
		}
		a.State.Finish(step)
	}

	if failures := a.J.Failures(); len(failures) > 0 {
		a.J.Info("", "finished in %s with %d problem(s): %s",
			time.Since(start).Round(time.Second), len(failures), strings.Join(failures, "; "))
	} else {
		a.J.Info("", "finished in %s with nothing to report", time.Since(start).Round(time.Second))
	}

	// The post-install check paints the result on the lock screen, so a bench
	// of machines can be read from the doorway. It runs last, because it
	// reports on everything above.
	if m.VerifyScript != "" {
		script := filepath.Join(dir, m.VerifyScript)
		if _, err := os.Stat(script); err == nil {
			r := run(15*time.Minute, "powershell", "-NoProfile", "-NonInteractive",
				"-ExecutionPolicy", "Bypass", "-File", script)
			a.J.Raw(r.Out)
			a.J.Info("", "post-install check exited %d", r.Code)
		} else {
			a.J.Fail("", "the post-install check %s is not on the machine", m.VerifyScript)
		}
	}

	a.J.Info("", "first-boot agent done")
	return nil
}

// Main is the agent's entry point, kept here so the command is three lines
// and the behaviour is testable.
func Main(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: dsky-agent apply [dir] | dsky-agent verify [dir] | dsky-agent user-install <job> <result>")
	}
	switch args[0] {
	case "apply":
		dir := ""
		if len(args) > 1 {
			dir = args[1]
		}
		if dir == "" {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			dir = filepath.Dir(exe)
		}
		return Apply(dir)
	case "verify":
		dir := ""
		if len(args) > 1 {
			dir = args[1]
		}
		if dir == "" {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			dir = filepath.Dir(exe)
		}
		checks, err := Verify(dir)
		if err != nil {
			return err
		}
		fmt.Printf("DSKY post-install check\n\n")
		if Report(checks, os.Stdout) {
			fmt.Println("\nThis machine matches the build.")
			return nil
		}
		fmt.Println("\nThis machine does not match the build; see the lines marked FAIL.")
		os.Exit(1)
		return nil
	case "user-install":
		// The unelevated half of an install that refuses an administrator.
		if len(args) != 3 {
			return fmt.Errorf("usage: dsky-agent user-install <job.json> <result.json>")
		}
		return RunUserJob(args[1], args[2])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
