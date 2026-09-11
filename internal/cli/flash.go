package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DustanBaker/the-composer/internal/compose"
	"github.com/DustanBaker/the-composer/internal/device"
	"github.com/DustanBaker/the-composer/internal/elevate"
	"github.com/DustanBaker/the-composer/internal/flash"
)

// workerJob is handed to the elevated flash worker via a temp file.
type workerJob struct {
	Artifact compose.Artifact `json:"artifact"`
	Device   device.Device    `json:"device"`
}

type workerEvent struct {
	Stage string `json:"stage"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
	Error string `json:"error,omitempty"`
}

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
	if !*yes {
		want := dev.SizeConfirmation()
		fmt.Printf("Type the device size (%s) to confirm: ", want)
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return fmt.Errorf("confirmation aborted: %w", err)
		}
		if strings.TrimSpace(line) != want {
			return fmt.Errorf("confirmation mismatch — aborting, nothing written")
		}
	}

	// Elevated already (or root): flash in-process.
	if elevate.IsElevated() {
		prog := &stageProgress{}
		err := flash.Flash(ctx, art, dev, prog.report)
		prog.finish()
		if err != nil {
			return err
		}
		fmt.Printf("Done. %s is written and verified — safe to remove.\n", dev.ID)
		return nil
	}

	// Otherwise hand off to the elevated worker and tail its progress.
	jobDir := os.TempDir()
	jobFile, err := os.CreateTemp(jobDir, "composer-job-*.json")
	if err != nil {
		return err
	}
	jobPath := jobFile.Name()
	progPath := jobPath + ".progress"
	defer os.Remove(jobPath)
	defer os.Remove(progPath)
	if err := json.NewEncoder(jobFile).Encode(workerJob{Artifact: *art, Device: dev}); err != nil {
		jobFile.Close()
		return err
	}
	if err := jobFile.Close(); err != nil {
		return err
	}

	fmt.Println("elevating flash worker —", elevate.Hint())
	done := make(chan struct{})
	go tailProgress(progPath, done)
	code, err := elevate.RunElevated([]string{"flash-worker", "--job", jobPath, "--progress", progPath})
	close(done)
	time.Sleep(150 * time.Millisecond) // let the tailer print the final event
	if err != nil {
		return err
	}
	if code != 0 {
		if msg := lastWorkerError(progPath); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return fmt.Errorf("flash worker exited with code %d", code)
	}
	fmt.Printf("\nDone. %s is written and verified — safe to remove.\n", dev.ID)
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
	b, err := os.ReadFile(*jobPath)
	if err != nil {
		return 2
	}
	var job workerJob
	if err := json.Unmarshal(b, &job); err != nil {
		return 2
	}
	prog, err := os.OpenFile(*progPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 2
	}
	defer prog.Close()
	enc := json.NewEncoder(prog)
	last := time.Now()
	emit := func(ev workerEvent) {
		enc.Encode(ev)
	}
	err = flash.Flash(ctx, &job.Artifact, job.Device, func(stage string, done, total int64) {
		if stage == "done" || time.Since(last) > 300*time.Millisecond {
			emit(workerEvent{Stage: stage, Done: done, Total: total})
			last = time.Now()
		}
	})
	if err != nil {
		emit(workerEvent{Stage: "error", Error: err.Error()})
		return 1
	}
	emit(workerEvent{Stage: "done"})
	return 0
}

// tailProgress renders worker events until done closes.
func tailProgress(path string, done <-chan struct{}) {
	var offset int64
	prog := &stageProgress{}
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-done:
			prog.finish()
			return
		case <-tick.C:
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		if _, err := f.Seek(offset, 0); err != nil {
			f.Close()
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			offset += int64(len(sc.Bytes())) + 1
			var ev workerEvent
			if json.Unmarshal(sc.Bytes(), &ev) != nil {
				continue
			}
			if ev.Error != "" {
				prog.finish()
				fmt.Fprintln(os.Stderr, "worker:", ev.Error)
				continue
			}
			prog.report(ev.Stage, ev.Done, ev.Total)
		}
		f.Close()
	}
}

func lastWorkerError(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	msg := ""
	for _, line := range strings.Split(string(b), "\n") {
		var ev workerEvent
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Error != "" {
			msg = ev.Error
		}
	}
	return msg
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
