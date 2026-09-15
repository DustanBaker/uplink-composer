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
	UI       *screen

	// rebootWanted is set by a step that Windows told to restart the
	// machine, with the reason in the operator's words. It is acted on
	// between steps, never in the middle of one.
	rebootWanted string
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
		// Windows says "Access is denied." and leaves the operator to work
		// out whose. C:\Windows\Setup\Scripts is writable only by an
		// administrator, and the agent needs to write there before it can
		// say anything at all -- including this.
		if os.IsPermission(err) {
			return fmt.Errorf("%s cannot be written to: run this from a PowerShell or Command Prompt "+
				"started with Run as administrator", dir)
		}
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

	// What the person at the machine sees. A nil screen -- no window, or not
	// Windows -- costs nothing: every call on it does nothing.
	a.UI = openScreen(machine)
	if a.UI == nil {
		a.J.Info("", "no status window on this machine; the log is the only record")
	}
	defer a.UI.Close()

	// Before anything else, arrange to be started again. Everything below
	// can be interrupted -- a restart Windows asks for, a machine that
	// crashes under a driver, somebody closing the lid -- and the state
	// file that lets this run carry on is worth nothing if nothing ever
	// runs the agent a second time.
	if err := ensureResumeFn(a); err != nil {
		a.J.FailDetail("", "this machine will not carry on by itself if it restarts before the end", err.Error())
	}

	for _, step := range m.Steps {
		if a.State.Finished(step) {
			a.J.Info(step, "already done on an earlier boot, skipping")
			continue
		}
		before := len(a.J.Failures())
		a.UI.Doing(step, "")
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
		a.UI.Finished(step, len(a.J.Failures())-before)
		a.State.Finish(step)
		// A restart is taken between steps, with the finished step
		// recorded, so the machine comes back and carries on at the next
		// one rather than repeating this one.
		if a.rebootWanted != "" && a.restartAndResume() {
			return nil
		}
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
		a.UI.Doing("checking", "reading back what this machine actually has")
		script := filepath.Join(dir, m.VerifyScript)
		if _, err := os.Stat(script); err == nil {
			r := run(15*time.Minute, "powershell", "-NoProfile", "-NonInteractive",
				"-ExecutionPolicy", "Bypass", "-File", script)
			a.J.Raw(r.Out)
			a.J.Info("", "post-install check exited %d", r.Code)
		} else {
			a.J.Fail("", "the post-install check %s is not on the machine", m.VerifyScript)
		}
		a.UI.Finished("checking", 0)
	}

	// Finished for good: stop asking to be started again, and hand the
	// machine over without the automatic sign-in provisioning needed.
	a.finishUp()
	a.J.Info("", "first-boot agent done")

	// The last thing on the screen is what this machine got, and it stays
	// there until somebody says they have seen it. Nothing is waiting on
	// this: the work is over.
	a.UI.Summary(summaryHeading(a.J.Failures()), summaryLines(a.J.Failures(), time.Since(start)))
	a.UI.WaitDismiss()
	return nil
}

// summaryHeading is the first thing read from across a room.
func summaryHeading(failures []string) string {
	if len(failures) == 0 {
		return "This machine is ready"
	}
	return "Finished, with " + itoa(len(failures)) + " problem(s)"
}

// summaryLines say what went wrong, plainly, and never more than fits.
func summaryLines(failures []string, took time.Duration) []string {
	if len(failures) == 0 {
		return []string{"Everything the build asked for is installed.",
			"Set up in " + shortDur(took) + "."}
	}
	const most = 8
	out := make([]string, 0, most+2)
	for i, f := range failures {
		if i == most {
			out = append(out, "and "+itoa(len(failures)-most)+" more, in firstboot.log")
			break
		}
		out = append(out, f)
	}
	return append(out, "Everything else is installed. The full record is in firstboot.log.")
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
