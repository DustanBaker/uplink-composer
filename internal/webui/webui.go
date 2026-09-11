// Package webui serves the local control page: pick a recipe, build, pick a
// device, arm, flash — with live progress. Binds loopback only, never runs
// elevated (flashes go through the same elevated worker as the CLI).
package webui

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DustanBaker/the-composer/internal/compose"
	"github.com/DustanBaker/the-composer/internal/device"
	"github.com/DustanBaker/the-composer/internal/flashrun"
	"github.com/DustanBaker/the-composer/internal/jobs"
	"github.com/DustanBaker/the-composer/internal/library"
	"github.com/DustanBaker/the-composer/internal/workspace"
)

//go:embed index.html
var indexHTML []byte

// Server holds the wiring for one serve session.
type Server struct {
	WS      *workspace.Workspace
	Lib     *library.Library
	CLIVars map[string]string
	Token   string
	Reg     *jobs.Registry

	deviceMu sync.Mutex // one raw-device operation at a time
}

// Serve runs until ctx is canceled.
func Serve(ctx context.Context, addr string, s *Server) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})
	mux.HandleFunc("GET /api/state", s.auth(s.handleState))
	mux.HandleFunc("POST /api/build", s.auth(s.handleBuild))
	mux.HandleFunc("POST /api/flash", s.auth(s.handleFlash))
	mux.HandleFunc("POST /api/capture", s.auth(s.handleCapture))
	mux.HandleFunc("GET /api/events", s.auth(s.handleEvents))
	return s.hostGuard(mux)
}

// hostGuard kills DNS-rebinding: only loopback Host headers are served, and
// mutating requests must carry a same-origin (or no) Origin.
func (s *Server) hostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			host = h
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				oh := ""
				if err == nil {
					oh = u.Hostname()
				}
				if oh != "127.0.0.1" && oh != "localhost" && oh != "::1" {
					http.Error(w, "forbidden origin", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("X-Composer-Token")
		if subtle.ConstantTimeCompare([]byte(tok), []byte(s.Token)) != 1 {
			http.Error(w, "missing or wrong token — open the exact URL composer serve printed", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, status int, format string, args ...any) {
	writeJSON(w, status, map[string]string{"error": fmt.Sprintf(format, args...)})
}

// ── state ────────────────────────────────────────────────────────────────────

type stateResp struct {
	Org       string         `json:"org"`
	Workspace string         `json:"workspace"`
	Recipes   []recipeInfo   `json:"recipes"`
	Sources   []sourceInfo   `json:"sources"`
	Devices   []deviceInfo   `json:"devices"`
	Artifacts []artifactInfo `json:"artifacts"`
}

type recipeInfo struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Type string   `json:"type"`
	Lint []string `json:"lint,omitempty"`
}

type sourceInfo struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Pinned    bool   `json:"pinned"`
	InLibrary bool   `json:"in_library"`
	SizeMB    int64  `json:"size_mb,omitempty"`
}

type deviceInfo struct {
	ID        string   `json:"id"`
	Model     string   `json:"model"`
	Bus       string   `json:"bus"`
	SizeGiB   string   `json:"size_gib"`
	Flashable bool     `json:"flashable"`
	System    bool     `json:"system"`
	Mounts    []string `json:"mounts,omitempty"`
	Confirm   string   `json:"confirm"`
}

type artifactInfo struct {
	Path     string `json:"path"`
	RecipeID string `json:"recipe_id"`
	SizeMB   int64  `json:"size_mb"`
	Created  string `json:"created"`
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	resp := stateResp{Org: s.WS.Config.Org.Name, Workspace: s.WS.Dir,
		Recipes: []recipeInfo{}, Sources: []sourceInfo{}, Devices: []deviceInfo{}, Artifacts: []artifactInfo{}}

	recipes, err := s.WS.Recipes()
	if err != nil {
		httpErr(w, 500, "loading recipes: %v", err)
		return
	}
	for _, rc := range recipes {
		info := recipeInfo{ID: rc.ID, Name: rc.Name, Type: string(rc.OS.Type)}
		for _, f := range rc.Lint() {
			info.Lint = append(info.Lint, f.String())
		}
		resp.Recipes = append(resp.Recipes, info)
	}

	if sources, err := s.WS.Sources(); err == nil {
		for _, src := range sources {
			info := sourceInfo{ID: src.ID, Kind: string(src.Kind), Pinned: src.SHA256 != ""}
			if e, err := s.Lib.Resolve(src.ID); err == nil {
				info.InLibrary = true
				info.SizeMB = e.Size >> 20
			}
			resp.Sources = append(resp.Sources, info)
		}
	}

	if devs, err := device.List(r.Context()); err == nil {
		for _, d := range devs {
			resp.Devices = append(resp.Devices, deviceInfo{
				ID: d.ID, Model: d.Model, Bus: d.Bus,
				SizeGiB:   d.SizeConfirmation(),
				Flashable: d.Flashable(), System: d.System,
				Mounts: d.Mounts, Confirm: d.SizeConfirmation(),
			})
		}
	}

	if metas, err := filepath.Glob(filepath.Join(s.Lib.ArtifactsDir(), "*.img.json")); err == nil {
		for _, m := range metas {
			a, err := compose.LoadArtifact(m)
			if err != nil {
				continue
			}
			if _, err := os.Stat(a.Path); err != nil {
				continue
			}
			resp.Artifacts = append(resp.Artifacts, artifactInfo{
				Path: a.Path, RecipeID: a.RecipeID, SizeMB: a.Size >> 20,
				Created: a.CreatedAt.Format("2006-01-02 15:04"),
			})
		}
		sort.Slice(resp.Artifacts, func(i, j int) bool { return resp.Artifacts[i].Created > resp.Artifacts[j].Created })
	}
	writeJSON(w, 200, resp)
}

// ── build ────────────────────────────────────────────────────────────────────

func (s *Server) handleBuild(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Recipe string `json:"recipe"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Recipe == "" {
		httpErr(w, 400, "body must be {\"recipe\": \"<id>\"}")
		return
	}
	job := s.Reg.New("build", req.Recipe)
	go func() {
		art, err := s.build(context.Background(), req.Recipe, progressFor(job))
		if err != nil {
			job.Fail(err)
			return
		}
		job.Finish(art.Path)
	}()
	writeJSON(w, 202, map[string]string{"job_id": job.ID})
}

func (s *Server) build(ctx context.Context, recipeID string, progress func(string, int64, int64)) (*compose.Artifact, error) {
	rc, err := s.WS.Recipe(recipeID)
	if err != nil {
		return nil, err
	}
	for _, f := range rc.Lint() {
		if f.Severity == "error" {
			return nil, fmt.Errorf("lint: %s", f.Message)
		}
	}
	return compose.Build(ctx, compose.Request{
		Workspace: s.WS, Library: s.Lib, Recipe: rc, CLIVars: s.CLIVars,
		Progress: progress,
	})
}

// buildProgressJob wraps compose progress into job events.
func progressFor(job *jobs.Job) func(stage string, done, total int64) {
	return func(stage string, done, total int64) { job.Progress(stage, done, total) }
}

// ── flash ────────────────────────────────────────────────────────────────────

func (s *Server) handleFlash(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Recipe   string `json:"recipe,omitempty"`
		Artifact string `json:"artifact,omitempty"`
		DeviceID string `json:"device_id"`
		Confirm  string `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DeviceID == "" {
		httpErr(w, 400, "body must include device_id, confirm, and recipe or artifact")
		return
	}
	if (req.Recipe == "") == (req.Artifact == "") {
		httpErr(w, 400, "exactly one of recipe or artifact is required")
		return
	}

	// Re-enumerate NOW; never trust a stale listing for a destructive op.
	dev, err := s.findDevice(r.Context(), req.DeviceID)
	if err != nil {
		httpErr(w, 400, "%v", err)
		return
	}
	if !dev.Flashable() {
		httpErr(w, 400, "%s is not flashable (bus=%s, system=%v)", dev.ID, dev.Bus, dev.System)
		return
	}
	// Server-side arm check: the typed size must match.
	if strings.TrimSpace(req.Confirm) != dev.SizeConfirmation() {
		httpErr(w, 400, "confirmation mismatch: device %s is %s GiB — type exactly %q to arm",
			dev.ID, dev.SizeConfirmation(), dev.SizeConfirmation())
		return
	}

	title := req.Recipe
	if title == "" {
		title = filepath.Base(req.Artifact)
	}
	job := s.Reg.New("flash", fmt.Sprintf("%s → %s", title, dev.ID))
	go func() {
		s.deviceMu.Lock()
		defer s.deviceMu.Unlock()
		var art *compose.Artifact
		var err error
		if req.Artifact != "" {
			art, err = compose.LoadArtifact(compose.MetaPath(req.Artifact))
		} else {
			art, err = s.build(context.Background(), req.Recipe, progressFor(job))
		}
		if err != nil {
			job.Fail(err)
			return
		}
		if err := flashrun.RunFlash(context.Background(), art, dev, progressFor(job)); err != nil {
			job.Fail(err)
			return
		}
		job.Finish("flashed and verified — safe to remove")
	}()
	writeJSON(w, 202, map[string]string{"job_id": job.ID})
}

func (s *Server) handleCapture(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID string `json:"device_id"`
		Confirm  string `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DeviceID == "" {
		httpErr(w, 400, "body must include device_id and confirm")
		return
	}
	dev, err := s.findDevice(r.Context(), req.DeviceID)
	if err != nil {
		httpErr(w, 400, "%v", err)
		return
	}
	if dev.System {
		httpErr(w, 400, "refusing to capture the system disk")
		return
	}
	if strings.TrimSpace(req.Confirm) != dev.SizeConfirmation() {
		httpErr(w, 400, "confirmation mismatch: device is %s GiB", dev.SizeConfirmation())
		return
	}
	outPath := filepath.Join(s.Lib.ArtifactsDir(), fmt.Sprintf("capture-%s.img", time.Now().Format("20060102-150405")))
	job := s.Reg.New("capture", fmt.Sprintf("%s → %s", dev.ID, filepath.Base(outPath)))
	go func() {
		s.deviceMu.Lock()
		defer s.deviceMu.Unlock()
		result, err := flashrun.RunCapture(context.Background(), dev, outPath, progressFor(job))
		if err != nil {
			job.Fail(err)
			return
		}
		job.Finish(outPath + " (" + result + ")")
	}()
	writeJSON(w, 202, map[string]string{"job_id": job.ID})
}

func (s *Server) findDevice(ctx context.Context, id string) (device.Device, error) {
	devs, err := device.List(ctx)
	if err != nil {
		return device.Device{}, err
	}
	for _, d := range devs {
		if strings.EqualFold(d.ID, id) {
			return d, nil
		}
	}
	return device.Device{}, fmt.Errorf("device %s is no longer attached — refresh and retry", id)
}

// ── events (SSE) ─────────────────────────────────────────────────────────────

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpErr(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	ch, cancel := s.Reg.Subscribe()
	defer cancel()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case ev, open := <-ch:
			if !open {
				return
			}
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
	}
}
