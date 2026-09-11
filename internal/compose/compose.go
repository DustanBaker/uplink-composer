// Package compose turns a recipe into a flashable artifact: for Windows
// media, a raw MBR/GPT+FAT32 image built in userspace; for Linux ISOs and
// appliance images, a reference to the (possibly compressed) blob that the
// flash engine raw-writes.
package compose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/DustanBaker/the-composer/internal/buildinfo"
	"github.com/DustanBaker/the-composer/internal/library"
	"github.com/DustanBaker/the-composer/internal/recipe"
	"github.com/DustanBaker/the-composer/internal/workspace"
)

// defaultSourceDateEpoch fixes FAT timestamps for reproducible images when
// the caller has not set SOURCE_DATE_EPOCH (2025-09-02, the first NUC
// stick's completion date).
const defaultSourceDateEpoch = "1756800000"

// Request is one build.
type Request struct {
	Workspace *workspace.Workspace
	Library   *library.Library
	Recipe    *recipe.Recipe
	CLIVars   map[string]string
	// Rebuild forces composing even when a cached artifact matches.
	Rebuild bool
	// Progress receives coarse stage updates; total may be -1.
	Progress func(stage string, done, total int64)
}

func (r *Request) progress(stage string, done, total int64) {
	if r.Progress != nil {
		r.Progress(stage, done, total)
	}
}

// Artifact is a flashable build product, persisted as a JSON sidecar so
// `composer flash` can run in a later invocation.
type Artifact struct {
	RecipeID  string    `json:"recipe_id"`
	Kind      string    `json:"kind"` // "image" (composed) | "raw" (blob passthrough)
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256,omitempty"` // of Path as stored
	Compress  string    `json:"compress,omitempty"` // raw kind: none|xz|zstd|gz
	InputsKey string    `json:"inputs_key"`
	Verify    string    `json:"verify"` // readback-sha256 | none
	MinStick  int64     `json:"min_stick,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Tool      string    `json:"tool"`
}

// MetaPath is the sidecar location for an artifact image path.
func MetaPath(imgPath string) string { return imgPath + ".json" }

// LoadArtifact reads a sidecar.
func LoadArtifact(metaPath string) (*Artifact, error) {
	b, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, err
	}
	var a Artifact
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, fmt.Errorf("%s: %w", metaPath, err)
	}
	return &a, nil
}

func (a *Artifact) save() error {
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(MetaPath(a.Path), b, 0o644)
}

// Build dispatches on recipe OS type.
func Build(ctx context.Context, req Request) (*Artifact, error) {
	switch req.Recipe.OS.Type {
	case recipe.OSWindows:
		return buildWindows(ctx, req)
	case recipe.OSLinuxISO, recipe.OSRawImg:
		return buildRaw(ctx, req)
	default:
		return nil, fmt.Errorf("compose: unsupported os.type %q", req.Recipe.OS.Type)
	}
}

// inputsKey fingerprints everything that shapes the output: the recipe file,
// the workspace config, resolved source hashes, and the tool version.
func inputsKey(req Request, sourceHashes ...string) (string, error) {
	h := sha256.New()
	for _, p := range []string{req.Recipe.Path, filepath.Join(req.Workspace.Dir, "workspace.yaml")} {
		b, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		h.Write(b)
		h.Write([]byte{0})
	}
	// Template files referenced by the recipe.
	if w := req.Recipe.Windows; w != nil {
		var tmpls []string
		if w.Unattend != nil && w.Unattend.Template != "" {
			tmpls = append(tmpls, w.Unattend.Template)
		}
		if w.Firstboot.Template != "" {
			tmpls = append(tmpls, w.Firstboot.Template)
		}
		for _, t := range tmpls {
			b, err := os.ReadFile(filepath.Join(req.Workspace.Dir, filepath.FromSlash(t)))
			if err != nil {
				return "", err
			}
			h.Write(b)
			h.Write([]byte{0})
		}
	}
	for _, s := range sourceHashes {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	// Vars affect rendering (vars.local.yaml and --var included).
	vars := req.Workspace.MergedVars(req.Recipe, req.CLIVars)
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(h, "%s=%s\x00", k, vars[k])
	}
	h.Write([]byte(buildinfo.Version))
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// hashFileWithProgress hashes a file reporting progress.
func hashFileWithProgress(path string, report func(done, total int64)) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	buf := make([]byte, 4<<20)
	var done int64
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
			done += int64(n)
			if report != nil {
				report(done, st.Size())
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", 0, rerr
		}
	}
	return hex.EncodeToString(h.Sum(nil)), st.Size(), nil
}

func minStickBytes(r *recipe.Recipe) int64 {
	if r.Target.MinStick == "" {
		return 0
	}
	n, err := recipe.ParseSize(r.Target.MinStick)
	if err != nil {
		return 0
	}
	return n
}
