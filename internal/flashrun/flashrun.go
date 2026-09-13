// Package flashrun runs privileged device operations (flash, capture) from
// an unprivileged caller: in-process when already elevated, otherwise via a
// relaunched elevated worker that streams progress through a file. Shared by
// the CLI and the web UI.
package flashrun

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/uplinkresearch/dsky/internal/awake"
	"github.com/uplinkresearch/dsky/internal/compose"
	"github.com/uplinkresearch/dsky/internal/device"
	"github.com/uplinkresearch/dsky/internal/diskutil"
	"github.com/uplinkresearch/dsky/internal/elevate"
	"github.com/uplinkresearch/dsky/internal/flash"
)

// Progress mirrors flash.Progress.
type Progress func(stage string, done, total int64)

// Job is the file handed to the elevated worker. Several devices are written
// in one job on purpose: each would otherwise need its own elevation, and
// twenty UAC prompts to image twenty sticks is not a workflow anyone would
// use.
type Job struct {
	Op       string            `json:"op"` // "flash" | "clone" | "duplicate" | "prepare"
	Artifact *compose.Artifact `json:"artifact,omitempty"`
	Devices  []device.Device   `json:"devices"`
	OutPath  string            `json:"out_path,omitempty"` // clone
	Disk     *diskutil.Options `json:"disk,omitempty"`     // prepare
	// Source is the disk being copied for "duplicate"; Devices are where it
	// goes. It is read and never written, so it may be any disk at all.
	Source *device.Device `json:"source,omitempty"`
	// AllowFixed carries a human's decision to write to something that is not
	// removable media. It crosses the elevation boundary as part of the job
	// because the worker re-checks the policy itself rather than trusting that
	// the caller did.
	AllowFixed bool `json:"allow_fixed,omitempty"`
}

// event is one progress-file line. Device tags which target it came from, so
// the parent can render a row per stick.
type event struct {
	Stage  string `json:"stage"`
	Device string `json:"device,omitempty"`
	Done   int64  `json:"done"`
	Total  int64  `json:"total"`
	Error  string `json:"error,omitempty"`
	Result string `json:"result,omitempty"`
}

// DeviceProgress reports progress for one target among several.
type DeviceProgress func(deviceID, stage string, done, total int64)

// RunFlash writes art to dev, elevating as needed.
func RunFlash(ctx context.Context, art *compose.Artifact, dev device.Device, progress Progress) error {
	return RunFlashMany(ctx, art, []device.Device{dev}, func(_, stage string, done, total int64) {
		if progress != nil {
			progress(stage, done, total)
		}
	})
}

// RunFlashMany writes art to every device, in parallel, under a single
// elevation. A stick that fails does not stop the others: the returned error
// names every target that did not make it, and progress keeps reporting for
// the rest.
func RunFlashMany(ctx context.Context, art *compose.Artifact, devs []device.Device, progress DeviceProgress) error {
	if len(devs) == 0 {
		return fmt.Errorf("no devices to write")
	}
	if elevate.IsElevated() {
		return flashAll(ctx, art, devs, func(id, stage string, done, total int64) {
			if progress != nil {
				progress(id, stage, done, total)
			}
		})
	}
	_, err := runElevated(ctx, Job{Op: "flash", Artifact: art, Devices: devs}, progress)
	return err
}

// RunDuplicate copies src onto every disk in dsts, elevating as needed.
//
// allowFixed is the caller's statement that a human chose a destination which
// is not removable media. It is passed down rather than decided here, and the
// worker checks the policy again on the far side of elevation — a decision made
// in the UI should not become a decision made by whatever can reach this
// function.
func RunDuplicate(ctx context.Context, src device.Device, dsts []device.Device, allowFixed bool, progress DeviceProgress) error {
	if len(dsts) == 0 {
		return fmt.Errorf("no destination to copy onto")
	}
	if elevate.IsElevated() {
		_, err := flash.Clone(ctx, src, dsts, allowFixed,
			func(target, stage string, done, total int64) {
				if progress != nil {
					progress(target, stage, done, total)
				}
			})
		return err
	}
	_, err := runElevated(ctx, Job{Op: "duplicate", Source: &src, Devices: dsts, AllowFixed: allowFixed}, progress)
	return err
}

// RunClone reads dev into outPath (through the end of its last partition),
// elevating as needed. Returns the result summary (sha256:size).
func RunClone(ctx context.Context, dev device.Device, outPath string, progress Progress) (string, error) {
	if elevate.IsElevated() {
		return flash.Capture(ctx, dev, outPath, flash.Progress(progress))
	}
	return runElevated(ctx, Job{Op: "clone", Devices: []device.Device{dev}, OutPath: outPath},
		func(_, stage string, done, total int64) {
			if progress != nil {
				progress(stage, done, total)
			}
		})
}

// RunPrepare rewrites a removable disk's partition table and gives it one
// full-size formatted volume, elevating as needed.
func RunPrepare(ctx context.Context, dev device.Device, opts diskutil.Options, progress Progress) (string, error) {
	if elevate.IsElevated() {
		err := diskutil.Prepare(ctx, dev, opts, func(stage string) {
			if progress != nil {
				progress(stage, 0, -1)
			}
		})
		return "prepared", err
	}
	return runElevated(ctx, Job{Op: "prepare", Devices: []device.Device{dev}, Disk: &opts},
		func(_, stage string, done, total int64) {
			if progress != nil {
				progress(stage, done, total)
			}
		})
}

// flashAll writes to every device concurrently. Each target is independent —
// its own handle, its own readback verify — so one slow or dead stick only
// holds up itself.
func flashAll(ctx context.Context, art *compose.Artifact, devs []device.Device, progress DeviceProgress) error {
	// Held across the whole batch, in the process actually doing the writing.
	// A suspend partway through leaves a stick that mounts and boots to
	// something broken, which nobody thinks to suspect.
	defer awake.Keep("writing removable media")()

	var wg sync.WaitGroup
	errs := make([]error, len(devs))
	for i, dev := range devs {
		wg.Add(1)
		go func(i int, dev device.Device) {
			defer wg.Done()
			errs[i] = flash.Flash(ctx, art, dev, func(stage string, done, total int64) {
				progress(dev.ID, stage, done, total)
			})
			if errs[i] != nil {
				progress(dev.ID, "error: "+errs[i].Error(), 0, -1)
			} else {
				progress(dev.ID, "done", 1, 1)
			}
		}(i, dev)
	}
	wg.Wait()

	var failed []string
	for i, err := range errs {
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", devs[i].ID, err))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d failed:\n  %s", len(failed), len(devs), strings.Join(failed, "\n  "))
	}
	return nil
}

// runElevated hands the job to a UAC/pkexec-relaunched worker and tails its
// progress file, forwarding events to progress. Returns the worker's Result.
func runElevated(ctx context.Context, job Job, progress DeviceProgress) (string, error) {
	jobFile, err := os.CreateTemp("", "dsky-job-*.json")
	if err != nil {
		return "", err
	}
	jobPath := jobFile.Name()
	progPath := jobPath + ".progress"
	defer os.Remove(jobPath)
	defer os.Remove(progPath)
	if err := json.NewEncoder(jobFile).Encode(job); err != nil {
		jobFile.Close()
		return "", err
	}
	if err := jobFile.Close(); err != nil {
		return "", err
	}

	if progress != nil {
		progress("", "waiting for elevation approval", 0, -1)
	}
	done := make(chan struct{})
	var result string
	var workerErr string
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		result, workerErr = tail(ctx, progPath, done, progress)
	}()
	code, err := elevate.RunElevated([]string{"flash-worker", "--job", jobPath, "--progress", progPath})
	close(done)
	<-finished // the tailer drains the final lines, then publishes result/workerErr
	if err != nil {
		return "", err
	}
	if code != 0 {
		if workerErr != "" {
			return "", fmt.Errorf("%s", workerErr)
		}
		return "", fmt.Errorf("elevated worker exited with code %d (%s)", code, elevate.Hint())
	}
	return result, nil
}

// tail reads progress-file lines until done closes; returns result/error seen.
func tail(ctx context.Context, path string, done <-chan struct{}, progress DeviceProgress) (result, workerErr string) {
	var offset int64
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	drain := func() {
		f, err := os.Open(path)
		if err != nil {
			return
		}
		defer f.Close()
		if _, err := f.Seek(offset, 0); err != nil {
			return
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 64*1024)
		for sc.Scan() {
			offset += int64(len(sc.Bytes())) + 1
			var ev event
			if json.Unmarshal(sc.Bytes(), &ev) != nil {
				continue
			}
			if ev.Error != "" {
				workerErr = ev.Error
			}
			if ev.Result != "" {
				result = ev.Result
			}
			if progress != nil && ev.Stage != "" {
				progress(ev.Device, ev.Stage, ev.Done, ev.Total)
			}
		}
	}
	for {
		select {
		case <-done:
			drain()
			return result, workerErr
		case <-ctx.Done():
			return result, workerErr
		case <-tick.C:
			drain()
		}
	}
}

// Worker executes inside the elevated relaunch; the returned int is the
// process exit code.
func Worker(ctx context.Context, jobPath, progPath string) int {
	b, err := os.ReadFile(jobPath)
	if err != nil {
		return 2
	}
	var job Job
	if err := json.Unmarshal(b, &job); err != nil {
		return 2
	}
	prog, err := os.OpenFile(progPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 2
	}
	defer prog.Close()
	enc := json.NewEncoder(prog)
	// Targets are written concurrently, so the shared encoder and the
	// throttle clock both need guarding.
	var mu sync.Mutex
	emit := func(ev event) {
		mu.Lock()
		defer mu.Unlock()
		enc.Encode(ev)
	}
	last := map[string]time.Time{}
	report := func(devID, stage string, done, total int64) {
		mu.Lock()
		fresh := stage == "done" || strings.HasPrefix(stage, "error") || time.Since(last[devID]) > 250*time.Millisecond
		if fresh {
			last[devID] = time.Now()
		}
		mu.Unlock()
		if fresh {
			emit(event{Stage: stage, Device: devID, Done: done, Total: total})
		}
	}

	if len(job.Devices) == 0 {
		emit(event{Error: "job has no devices"})
		return 2
	}
	switch job.Op {
	case "flash":
		if job.Artifact == nil {
			emit(event{Error: "job has no artifact"})
			return 2
		}
		if err := flashAll(ctx, job.Artifact, job.Devices, report); err != nil {
			emit(event{Stage: "error", Error: err.Error()})
			return 1
		}
		emit(event{Stage: "done", Result: fmt.Sprintf("flashed %d", len(job.Devices))})
		return 0
	case "clone":
		result, err := flash.Capture(ctx, job.Devices[0], job.OutPath, func(stage string, done, total int64) {
			report(job.Devices[0].ID, stage, done, total)
		})
		if err != nil {
			emit(event{Stage: "error", Error: err.Error()})
			return 1
		}
		emit(event{Stage: "done", Result: result})
		return 0
	case "duplicate":
		if job.Source == nil {
			emit(event{Error: "job has no source disk"})
			return 2
		}
		res, err := flash.Clone(ctx, *job.Source, job.Devices, job.AllowFixed,
			func(target, stage string, done, total int64) { report(target, stage, done, total) })
		// Report each destination's own outcome before the overall one: with
		// twenty sticks, "one failed" is useless without saying which.
		for _, r := range res {
			if r.Err != nil {
				emit(event{Stage: "error", Device: r.Device.ID, Error: r.Err.Error()})
			}
		}
		if err != nil {
			emit(event{Stage: "error", Error: err.Error()})
			return 1
		}
		emit(event{Stage: "done", Result: fmt.Sprintf("copied onto %d", len(res))})
		return 0
	case "prepare":
		if job.Disk == nil {
			emit(event{Error: "job has no disk options"})
			return 2
		}
		dev := job.Devices[0]
		err := diskutil.Prepare(ctx, dev, *job.Disk, func(stage string) {
			report(dev.ID, stage, 0, -1)
		})
		if err != nil {
			emit(event{Stage: "error", Error: err.Error()})
			return 1
		}
		emit(event{Stage: "done", Result: "prepared " + dev.ID})
		return 0
	default:
		emit(event{Error: fmt.Sprintf("unknown op %q", job.Op)})
		return 2
	}
}
