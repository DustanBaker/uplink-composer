package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/uplinkresearch/dsky/internal/device"
	"github.com/uplinkresearch/dsky/internal/diskutil"
	"github.com/uplinkresearch/dsky/internal/flash"
	"github.com/uplinkresearch/dsky/internal/flashrun"
	"github.com/uplinkresearch/dsky/internal/jobs"
)

// handleIdentify describes one disk well enough for somebody to recognise it
// before erasing it.
//
// Read-only and unelevated on purpose. Making someone approve a prompt in order
// to look at a disk teaches them to approve prompts, which is the opposite of
// what the confirmation before a destructive write depends on.
func (s *Server) handleIdentify(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("device")
	if id == "" {
		httpErr(w, 400, "device is required")
		return
	}
	dev, err := s.findDevice(r.Context(), id)
	if err != nil {
		httpErr(w, 400, "%v", err)
		return
	}
	// Inspection can fail on a disk this system cannot read, which is itself
	// worth showing beside a destructive button rather than swallowing.
	layout, inspectErr := diskutil.Inspect(r.Context(), dev)
	var l diskutil.Layout
	if layout != nil {
		l = *layout
	}
	writeJSON(w, 200, identityJSON(diskutil.Identify(dev, l, inspectErr)))
}

type identityOut struct {
	DeviceID    string   `json:"device_id"`
	Headline    string   `json:"headline"`
	Kind        string   `json:"kind"`
	Contents    []string `json:"contents"`
	Warnings    []string `json:"warnings"`
	Destructive bool     `json:"destructive"`
	Routine     bool     `json:"routine"`
	Writable    bool     `json:"writable"`
	SizeBytes   int64    `json:"size_bytes"`
	Confirm     string   `json:"confirm"`
}

func identityJSON(id diskutil.Identity) identityOut {
	return identityOut{
		DeviceID: id.Device.ID, Headline: id.Headline, Kind: id.Kind,
		Contents: id.Contents, Warnings: id.Warnings,
		Destructive: id.Destructive, Routine: id.Routine,
		Writable:  id.Device.Writable(),
		SizeBytes: id.Device.SizeBytes,
		Confirm:   id.Device.SizeConfirmation(),
	}
}

// handleDuplicate copies one disk onto one or more others.
func (s *Server) handleDuplicate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SourceID  string   `json:"source_id"`
		TargetIDs []string `json:"target_ids"`
		Confirm   string   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SourceID == "" || len(req.TargetIDs) == 0 {
		httpErr(w, 400, "body must include source_id, target_ids and confirm")
		return
	}
	src, err := s.findDevice(r.Context(), req.SourceID)
	if err != nil {
		httpErr(w, 400, "%v", err)
		return
	}

	var dsts []device.Device
	allowFixed := false
	for _, id := range req.TargetIDs {
		d, err := s.findDevice(r.Context(), id)
		if err != nil {
			httpErr(w, 400, "%v", err)
			return
		}
		// The request never carries "allow fixed disks" as a flag of its own.
		// It is derived from the disks actually named, so a client cannot widen
		// the policy beyond the ones the person in front of it confirmed.
		if !d.Routine() {
			allowFixed = true
		}
		if err := flash.CheckTarget(d, true); err != nil {
			httpErr(w, 400, "%v", err)
			return
		}
		dsts = append(dsts, d)
	}

	// One typed confirmation per destination, because the thing being confirmed
	// is which disks these are. A single box covering several would be one
	// answer standing in for several different questions.
	want := confirmationFor(dsts)
	if strings.TrimSpace(req.Confirm) != want {
		httpErr(w, 400, "confirmation mismatch: type exactly %q to arm this copy", want)
		return
	}

	what := fmt.Sprintf("%s → %d disk", src.ID, len(dsts))
	if len(dsts) != 1 {
		what += "s"
	}
	job := s.Reg.New("duplicate", what)
	go func() {
		s.deviceMu.Lock()
		defer s.deviceMu.Unlock()
		err := flashrun.RunDuplicate(context.Background(), src, dsts, allowFixed, deviceProgressFor(job))
		if err != nil {
			job.Fail(err)
			return
		}
		job.Finish(fmt.Sprintf("copied and verified onto %d", len(dsts)))
	}()
	writeJSON(w, 202, map[string]string{"job_id": job.ID})
}

// deviceProgressFor folds per-destination progress into one job.
//
// Clone calls its progress function from one goroutine per destination, so this
// must be safe to call concurrently; jobs.Job.Progress is. The stage carries
// the disk it belongs to, because "62%" means nothing when twenty sticks are
// writing at once.
func deviceProgressFor(job *jobs.Job) flashrun.DeviceProgress {
	return func(deviceID, stage string, done, total int64) {
		if deviceID != "" {
			stage = short(deviceID) + " " + stage
		}
		job.Progress(stage, done, total)
	}
}

// short trims a device path down to the part a person reads.
func short(id string) string {
	if i := strings.LastIndexAny(id, `\/`); i >= 0 && i+1 < len(id) {
		return id[i+1:]
	}
	return id
}

// confirmationFor is the string someone types to arm a copy: every
// destination's size, in order, joined. It is deliberately more tedious with
// more disks — twenty sticks is twenty chances to have grabbed the wrong one.
func confirmationFor(dsts []device.Device) string {
	parts := make([]string, 0, len(dsts))
	for _, d := range dsts {
		parts = append(parts, d.SizeConfirmation())
	}
	return strings.Join(parts, " ")
}
