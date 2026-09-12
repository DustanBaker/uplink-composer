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
	"time"

	"github.com/DustanBaker/uplink-composer/internal/compose"
	"github.com/DustanBaker/uplink-composer/internal/device"
	"github.com/DustanBaker/uplink-composer/internal/elevate"
	"github.com/DustanBaker/uplink-composer/internal/flash"
)

// Progress mirrors flash.Progress.
type Progress func(stage string, done, total int64)

// Job is the file handed to the elevated worker.
type Job struct {
	Op       string            `json:"op"` // "flash" | "capture"
	Artifact *compose.Artifact `json:"artifact,omitempty"`
	Device   device.Device     `json:"device"`
	OutPath  string            `json:"out_path,omitempty"` // capture
}

// event is one progress-file line.
type event struct {
	Stage  string `json:"stage"`
	Done   int64  `json:"done"`
	Total  int64  `json:"total"`
	Error  string `json:"error,omitempty"`
	Result string `json:"result,omitempty"`
}

// RunFlash writes art to dev, elevating as needed.
func RunFlash(ctx context.Context, art *compose.Artifact, dev device.Device, progress Progress) error {
	if elevate.IsElevated() {
		return flash.Flash(ctx, art, dev, flash.Progress(progress))
	}
	_, err := runElevated(ctx, Job{Op: "flash", Artifact: art, Device: dev}, progress)
	return err
}

// RunCapture reads dev into outPath (through the end of its last partition),
// elevating as needed. Returns the capture result summary (sha256:size).
func RunCapture(ctx context.Context, dev device.Device, outPath string, progress Progress) (string, error) {
	if elevate.IsElevated() {
		return flash.Capture(ctx, dev, outPath, flash.Progress(progress))
	}
	return runElevated(ctx, Job{Op: "capture", Device: dev, OutPath: outPath}, progress)
}

// runElevated hands the job to a UAC/pkexec-relaunched worker and tails its
// progress file, forwarding events to progress. Returns the worker's Result.
func runElevated(ctx context.Context, job Job, progress Progress) (string, error) {
	jobFile, err := os.CreateTemp("", "composer-job-*.json")
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
		progress("waiting for elevation approval", 0, -1)
	}
	done := make(chan struct{})
	var result string
	var workerErr string
	go func() {
		result, workerErr = tail(ctx, progPath, done, progress)
	}()
	code, err := elevate.RunElevated([]string{"flash-worker", "--job", jobPath, "--progress", progPath})
	close(done)
	time.Sleep(200 * time.Millisecond) // let the tailer drain the final lines
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
func tail(ctx context.Context, path string, done <-chan struct{}, progress Progress) (result, workerErr string) {
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
				progress(ev.Stage, ev.Done, ev.Total)
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
	last := time.Now()
	emit := func(ev event) { enc.Encode(ev) }
	report := func(stage string, done, total int64) {
		if stage == "done" || time.Since(last) > 250*time.Millisecond {
			emit(event{Stage: stage, Done: done, Total: total})
			last = time.Now()
		}
	}

	switch job.Op {
	case "flash":
		if job.Artifact == nil {
			emit(event{Error: "job has no artifact"})
			return 2
		}
		if err := flash.Flash(ctx, job.Artifact, job.Device, report); err != nil {
			emit(event{Stage: "error", Error: err.Error()})
			return 1
		}
		emit(event{Stage: "done", Result: "flashed"})
		return 0
	case "capture":
		result, err := flash.Capture(ctx, job.Device, job.OutPath, report)
		if err != nil {
			emit(event{Stage: "error", Error: err.Error()})
			return 1
		}
		emit(event{Stage: "done", Result: result})
		return 0
	default:
		emit(event{Error: fmt.Sprintf("unknown op %q", job.Op)})
		return 2
	}
}
