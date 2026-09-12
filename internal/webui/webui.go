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

	"github.com/DustanBaker/uplink-composer/internal/appcatalog"
	"github.com/DustanBaker/uplink-composer/internal/appconfig"
	"github.com/DustanBaker/uplink-composer/internal/compose"
	"github.com/DustanBaker/uplink-composer/internal/device"
	"github.com/DustanBaker/uplink-composer/internal/driverresolve"
	"github.com/DustanBaker/uplink-composer/internal/filepicker"
	"github.com/DustanBaker/uplink-composer/internal/flashrun"
	"github.com/DustanBaker/uplink-composer/internal/hwdetect"
	"github.com/DustanBaker/uplink-composer/internal/jobs"
	"github.com/DustanBaker/uplink-composer/internal/library"
	"github.com/DustanBaker/uplink-composer/internal/oscatalog"
	"github.com/DustanBaker/uplink-composer/internal/recipe"
	"github.com/DustanBaker/uplink-composer/internal/workspace"
)

//go:embed index.html
var indexHTML []byte

// Server holds the wiring for one serve session. The workspace is optional
// and switchable at runtime, so the app can launch to a home screen and let
// the operator open a workspace from the page.
type Server struct {
	Lib     *library.Library
	CLIVars map[string]string
	Token   string
	Reg     *jobs.Registry
	Cfg     *appconfig.Config

	wsMu sync.RWMutex
	ws   *workspace.Workspace

	quit     func()     // cancels Serve; set in Serve
	deviceMu sync.Mutex // one raw-device operation at a time
}

// SetWorkspaceDir loads the workspace rooted at dir and makes it current,
// recording it in the recents. Used at startup and by the page.
func (s *Server) SetWorkspaceDir(dir string) error {
	root, err := workspace.Find(dir)
	if err != nil {
		return err
	}
	ws, err := workspace.Load(root)
	if err != nil {
		return err
	}
	s.wsMu.Lock()
	s.ws = ws
	s.wsMu.Unlock()
	if s.Cfg != nil {
		s.Cfg.AddRecent(root)
	}
	return nil
}

func (s *Server) workspace() *workspace.Workspace {
	s.wsMu.RLock()
	defer s.wsMu.RUnlock()
	return s.ws
}

// WorkspaceName returns the current workspace's org name, or "" if none.
func (s *Server) WorkspaceName() string {
	if ws := s.workspace(); ws != nil {
		return ws.Config.Org.Name
	}
	return ""
}

// Serve runs on ln until ctx is canceled or the page requests quit.
func Serve(ctx context.Context, ln net.Listener, s *Server) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.quit = cancel
	srv := &http.Server{
		Handler:           s.handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		shutdownCtx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
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
	mux.HandleFunc("POST /api/workspace", s.auth(s.handleSetWorkspace))
	mux.HandleFunc("POST /api/browse", s.auth(s.handleBrowse))
	mux.HandleFunc("POST /api/build", s.auth(s.handleBuild))
	mux.HandleFunc("POST /api/flash", s.auth(s.handleFlash))
	mux.HandleFunc("POST /api/install", s.auth(s.handleInstall))
	mux.HandleFunc("GET /api/detect", s.auth(s.handleDetect))
	mux.HandleFunc("POST /api/capture", s.auth(s.handleCapture))
	mux.HandleFunc("POST /api/quit", s.auth(s.handleQuit))
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
		tok := r.Header.Get("X-Uplink-Token")
		if subtle.ConstantTimeCompare([]byte(tok), []byte(s.Token)) != 1 {
			http.Error(w, "missing or wrong token — open the exact URL uplink serve printed", http.StatusUnauthorized)
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
	Org          string         `json:"org"`
	Workspace    string         `json:"workspace"`
	HasWorkspace bool           `json:"has_workspace"`
	Recent       []recentWS     `json:"recent"`
	Recipes      []recipeInfo   `json:"recipes"`
	Sources      []sourceInfo   `json:"sources"`
	Devices      []deviceInfo   `json:"devices"`
	Artifacts    []artifactInfo `json:"artifacts"`
	Catalog      []catalogEntry `json:"catalog"`
	Apps         []appEntry     `json:"apps"`
	LibraryRoot  string         `json:"library_root"`
}

// appEntry is one program the install picker can offer.
type appEntry struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

type catalogEntry struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Family        string   `json:"family"`
	Category      string   `json:"category"`
	Arch          string   `json:"arch"`
	Version       string   `json:"version"`
	Notes         string   `json:"notes"`
	FirmwareNotes string   `json:"firmware_notes,omitempty"`
	Editions      []string `json:"editions,omitempty"`
}

type recentWS struct {
	Dir  string `json:"dir"`
	Name string `json:"name"`
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
	resp := stateResp{
		Recent: []recentWS{}, Recipes: []recipeInfo{}, Sources: []sourceInfo{},
		Devices: []deviceInfo{}, Artifacts: []artifactInfo{}, Catalog: []catalogEntry{},
		Apps: []appEntry{}, LibraryRoot: s.Lib.Root,
	}
	// Only programs with a Windows package: that is all Quick Install can do
	// for now, and an option that cannot run is worse than no option.
	for _, a := range appcatalog.Catalog() {
		if a.Winget != "" {
			resp.Apps = append(resp.Apps, appEntry{ID: a.ID, Name: a.Name, Category: a.Category})
		}
	}
	for _, e := range oscatalog.Catalog() {
		resp.Catalog = append(resp.Catalog, catalogEntry{
			ID: e.ID, Name: e.Name, Family: string(e.Family),
			Category: string(e.Group()), Arch: e.CPUArch(), Version: e.Version,
			Notes: e.Notes, FirmwareNotes: e.FirmwareNotes, Editions: e.Editions,
		})
	}
	if s.Cfg != nil {
		for _, dir := range s.Cfg.Recent {
			resp.Recent = append(resp.Recent, recentWS{Dir: dir, Name: workspaceName(dir)})
		}
	}

	ws := s.workspace()
	if ws != nil {
		resp.HasWorkspace = true
		resp.Org = ws.Config.Org.Name
		resp.Workspace = ws.Dir
		if recipes, err := ws.Recipes(); err == nil {
			for _, rc := range recipes {
				info := recipeInfo{ID: rc.ID, Name: rc.Name, Type: string(rc.OS.Type)}
				for _, f := range rc.Lint() {
					info.Lint = append(info.Lint, f.String())
				}
				resp.Recipes = append(resp.Recipes, info)
			}
		}
		if sources, err := ws.Sources(); err == nil {
			for _, src := range sources {
				info := sourceInfo{ID: src.ID, Kind: string(src.Kind), Pinned: src.SHA256 != ""}
				if e, err := s.Lib.Resolve(src.ID); err == nil {
					info.InLibrary = true
					info.SizeMB = e.Size >> 20
				}
				resp.Sources = append(resp.Sources, info)
			}
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
	ws := s.workspace()
	if ws == nil {
		return nil, fmt.Errorf("no workspace is open")
	}
	rc, err := ws.Recipe(recipeID)
	if err != nil {
		return nil, err
	}
	for _, f := range rc.Lint() {
		if f.Severity == "error" {
			return nil, fmt.Errorf("lint: %s", f.Message)
		}
	}
	return compose.Build(ctx, compose.Request{
		Workspace: ws, Library: s.Lib, Recipe: rc, CLIVars: s.CLIVars,
		Progress: progress,
	})
}

// workspaceName reads a workspace directory's org name for display, falling
// back to the folder name.
func workspaceName(dir string) string {
	if ws, err := workspace.Load(dir); err == nil && ws.Config.Org.Name != "" {
		return ws.Config.Org.Name
	}
	return filepath.Base(dir)
}

// handleSetWorkspace opens the workspace at the posted dir.
func (s *Server) handleSetWorkspace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dir string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Dir) == "" {
		httpErr(w, 400, "body must be {\"dir\": \"<path>\"}")
		return
	}
	if err := s.SetWorkspaceDir(strings.TrimSpace(req.Dir)); err != nil {
		httpErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

// handleQuit stops the server (the app's clean exit).
func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"ok": "1"})
	go func() {
		time.Sleep(150 * time.Millisecond)
		if s.quit != nil {
			s.quit()
		}
	}()
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

// handleInstall is Quick Install: build media for a catalog OS + options and
// flash it to the chosen stick — no workspace required.
func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OSID              string   `json:"os_id"`
		Edition           string   `json:"edition"`
		AccountMode       string   `json:"account_mode"`
		Debloat           string   `json:"debloat"`
		BypassRequirement bool     `json:"bypass_requirement"`
		Drivers           bool     `json:"drivers"`
		DriversFor        string   `json:"drivers_for"`
		Apps              []string `json:"apps"`
		ISO               string   `json:"iso"`
		DeviceID          string   `json:"device_id"`
		Confirm           string   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OSID == "" || req.DeviceID == "" {
		httpErr(w, 400, "body must include os_id, device_id, and confirm")
		return
	}
	e, ok := oscatalog.Get(req.OSID)
	if !ok {
		httpErr(w, 400, "unknown OS %q", req.OSID)
		return
	}
	dev, err := s.findDevice(r.Context(), req.DeviceID)
	if err != nil {
		httpErr(w, 400, "%v", err)
		return
	}
	if !dev.Flashable() {
		httpErr(w, 400, "%s is not flashable (bus=%s, system=%v)", dev.ID, dev.Bus, dev.System)
		return
	}
	if strings.TrimSpace(req.Confirm) != dev.SizeConfirmation() {
		httpErr(w, 400, "confirmation mismatch: device %s is %s GiB — type exactly %q to arm",
			dev.ID, dev.SizeConfirmation(), dev.SizeConfirmation())
		return
	}
	// An ISO the operator downloaded themselves, filed before the build so the
	// Microsoft fetch (rate-limited to about one a day per address) is skipped.
	if iso := strings.TrimSpace(req.ISO); iso != "" && !oscatalog.InLibrary(s.Lib, e) {
		if err := oscatalog.CheckISO(iso); err != nil {
			httpErr(w, 400, "%v", err)
			return
		}
		if _, err := oscatalog.ImportISO(s.Lib, e, iso); err != nil {
			httpErr(w, 400, "importing %s: %v", filepath.Base(iso), err)
			return
		}
	}

	var hw []recipe.HardwareSpec
	if req.Drivers && e.Family == oscatalog.Windows {
		h, err := hwdetect.Detect(r.Context())
		if err != nil {
			httpErr(w, 400, "hardware detection failed: %v", err)
			return
		}
		if hw = driverresolve.SpecsFor(h, e.DriverOS()); len(hw) == 0 {
			httpErr(w, 400, "nothing to resolve drivers for on this machine")
			return
		}
	}
	// Named models are additive with detection: pnputil installs only what
	// matches, so one stick can carry packs for several machines.
	for _, spec := range strings.Split(req.DriversFor, ",") {
		if strings.TrimSpace(spec) == "" {
			continue
		}
		if e.Family != oscatalog.Windows {
			httpErr(w, 400, "drivers for a named model is a Windows option")
			return
		}
		h, err := driverresolve.SpecForModel(spec, e.DriverOS())
		if err != nil {
			httpErr(w, 400, "%v", err)
			return
		}
		hw = append(hw, h)
	}
	if len(req.Apps) > 0 {
		if _, err := appcatalog.WingetIDs(req.Apps); err != nil {
			httpErr(w, 400, "%v", err)
			return
		}
	}
	opts := oscatalog.Options{
		Edition: req.Edition, AccountMode: req.AccountMode,
		Debloat: req.Debloat, BypassRequirement: req.BypassRequirement,
		Hardware: hw, Apps: req.Apps,
	}
	job := s.Reg.New("install", fmt.Sprintf("%s → %s", e.Name, dev.ID))
	go func() {
		s.deviceMu.Lock()
		defer s.deviceMu.Unlock()
		art, err := oscatalog.BuildQuick(context.Background(), s.Lib, e, opts, progressFor(job))
		if err != nil {
			job.Fail(err)
			return
		}
		if err := flashrun.RunFlash(context.Background(), art, dev, progressFor(job)); err != nil {
			job.Fail(err)
			return
		}
		job.Finish("installed — safe to remove and boot the target machine")
	}()
	writeJSON(w, 202, map[string]string{"job_id": job.ID})
}

// handleBrowse opens the host's own folder chooser and returns what was
// picked. The browser cannot produce an absolute path itself, and the server
// is on the same machine as the person clicking, so it asks the desktop.
func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	// Generous: this blocks while a person reads a dialog.
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	path, err := filepicker.PickFolder(ctx, "Select a workspace folder")
	switch {
	case errors.Is(err, filepicker.ErrCancelled):
		writeJSON(w, 200, map[string]string{"path": ""})
		return
	case errors.Is(err, filepicker.ErrUnavailable):
		httpErr(w, 501, "no folder chooser on this host — type the path instead")
		return
	case err != nil:
		httpErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, map[string]string{"path": path})
}

// handleDetect profiles the machine the server runs on, so the page can offer
// "include drivers for this computer" with the actual hardware named.
func (s *Server) handleDetect(w http.ResponseWriter, r *http.Request) {
	h, err := hwdetect.Detect(r.Context())
	if err != nil {
		httpErr(w, 501, "%v", err)
		return
	}
	type dev struct {
		Name  string `json:"name"`
		Brand string `json:"brand,omitempty"`
	}
	resp := struct {
		Vendor      string   `json:"vendor"`
		Model       string   `json:"model"`
		CPU         string   `json:"cpu"`
		Feed        string   `json:"feed,omitempty"`
		GPUs        []dev    `json:"gpus"`
		NICs        []dev    `json:"nics"`
		LookupIDs   []string `json:"lookup_ids"`
		DeviceCount int      `json:"device_count"`
	}{
		Vendor: h.Vendor, Model: h.Model, CPU: h.CPU, Feed: h.KnownVendor(),
		GPUs: []dev{}, NICs: []dev{},
		LookupIDs: h.DriverHWIDs(), DeviceCount: len(h.Devices),
	}
	for _, g := range h.GPUs {
		resp.GPUs = append(resp.GPUs, dev{Name: g.Name, Brand: g.GPUVendor})
	}
	for _, n := range h.NICs {
		resp.NICs = append(resp.NICs, dev{Name: n.Name})
	}
	writeJSON(w, 200, resp)
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
	outPath := filepath.Join(s.Lib.ArtifactsDir(), fmt.Sprintf("clone-%s.img", time.Now().Format("20060102-150405")))
	job := s.Reg.New("clone", fmt.Sprintf("%s → %s", dev.ID, filepath.Base(outPath)))
	go func() {
		s.deviceMu.Lock()
		defer s.deviceMu.Unlock()
		result, err := flashrun.RunClone(context.Background(), dev, outPath, progressFor(job))
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
