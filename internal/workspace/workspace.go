// Package workspace loads an org's composition workspace: a git-friendly
// directory of workspace.yaml, manifests/, recipes/, templates/, payload/,
// and gitignored vars.local.yaml. DSKY is org-agnostic; everything
// org-specific lives here.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/uplinkresearch/dsky/internal/manifest"
	"github.com/uplinkresearch/dsky/internal/recipe"
)

// Config is workspace.yaml.
type Config struct {
	Version int `yaml:"version"`
	Org     struct {
		Name string `yaml:"name"`
		ID   string `yaml:"id"`
	} `yaml:"org"`
	Defaults struct {
		Locale   string `yaml:"locale,omitempty"`
		Timezone string `yaml:"timezone,omitempty"`
	} `yaml:"defaults,omitempty"`
	Vars map[string]string `yaml:"vars,omitempty"`
}

// Workspace is a loaded workspace.
type Workspace struct {
	Dir       string
	Config    Config
	LocalVars map[string]string // vars.local.yaml (gitignored secrets)
}

// Find walks upward from dir looking for workspace.yaml.
func Find(dir string) (string, error) {
	d, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(d, "workspace.yaml")); err == nil {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("no workspace.yaml found in %s or any parent — run `dsky init` to create a workspace", dir)
		}
		d = parent
	}
}

// Load reads the workspace rooted at dir.
func Load(dir string) (*Workspace, error) {
	b, err := os.ReadFile(filepath.Join(dir, "workspace.yaml"))
	if err != nil {
		return nil, err
	}
	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("workspace.yaml: %w", err)
	}
	if cfg.Version != 1 {
		return nil, fmt.Errorf("workspace.yaml: version must be 1")
	}
	if cfg.Org.Name == "" {
		return nil, fmt.Errorf("workspace.yaml: org.name is required")
	}
	ws := &Workspace{Dir: dir, Config: cfg, LocalVars: map[string]string{}}
	if lb, err := os.ReadFile(filepath.Join(dir, "vars.local.yaml")); err == nil {
		if err := yaml.Unmarshal(lb, &ws.LocalVars); err != nil {
			return nil, fmt.Errorf("vars.local.yaml: %w", err)
		}
	}
	return ws, nil
}

// Sources loads every manifest in manifests/.
func (w *Workspace) Sources() ([]*manifest.Source, error) {
	return manifest.LoadDir(filepath.Join(w.Dir, "manifests"))
}

// Source finds one manifest by id.
func (w *Workspace) Source(id string) (*manifest.Source, error) {
	srcs, err := w.Sources()
	if err != nil {
		return nil, err
	}
	for _, s := range srcs {
		if s.ID == id {
			return s, nil
		}
	}
	return nil, fmt.Errorf("no manifest with id %q under %s", id, filepath.Join(w.Dir, "manifests"))
}

// Recipes loads every recipe in recipes/.
func (w *Workspace) Recipes() ([]*recipe.Recipe, error) {
	dir := filepath.Join(w.Dir, "recipes")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*recipe.Recipe
	seen := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml")) {
			continue
		}
		r, err := recipe.Load(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[r.ID]; dup {
			return nil, fmt.Errorf("%s: duplicate recipe id %q (also in %s)", name, r.ID, prev)
		}
		seen[r.ID] = name
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Recipe finds one recipe by id.
func (w *Workspace) Recipe(id string) (*recipe.Recipe, error) {
	rs, err := w.Recipes()
	if err != nil {
		return nil, err
	}
	for _, r := range rs {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, fmt.Errorf("no recipe %q in %s (dsky recipes list)", id, filepath.Join(w.Dir, "recipes"))
}

// MergedVars implements the precedence chain: workspace < recipe <
// vars.local.yaml < CLI --var. Unattend vars overlay on top at render time.
func (w *Workspace) MergedVars(r *recipe.Recipe, cli map[string]string) map[string]string {
	out := map[string]string{}
	// workspace.yaml defaults seed the lowest-precedence layer so templates
	// can rely on {{.Vars.locale}} / {{.Vars.timezone}} without every
	// recipe repeating them.
	if w.Config.Defaults.Locale != "" {
		out["locale"] = w.Config.Defaults.Locale
	}
	if w.Config.Defaults.Timezone != "" {
		out["timezone"] = w.Config.Defaults.Timezone
	}
	for k, v := range w.Config.Vars {
		out[k] = v
	}
	if r != nil {
		for k, v := range r.Vars {
			out[k] = v
		}
	}
	for k, v := range w.LocalVars {
		out[k] = v
	}
	for k, v := range cli {
		out[k] = v
	}
	return out
}

// Org returns template identity info.
func (w *Workspace) Org() recipe.OrgInfo {
	return recipe.OrgInfo{Name: w.Config.Org.Name, ID: w.Config.Org.ID}
}

// LintAll runs workspace-wide checks: per-recipe lint plus repo hygiene
// (no tracked file over sizeCapMB — big binaries belong in manifests).
func (w *Workspace) LintAll(sizeCapMB int64) (map[string][]recipe.Finding, error) {
	if sizeCapMB <= 0 {
		sizeCapMB = 5
	}
	out := map[string][]recipe.Finding{}
	rs, err := w.Recipes()
	if err != nil {
		return nil, err
	}
	for _, r := range rs {
		if fs := r.Lint(); len(fs) > 0 {
			out[r.ID] = fs
		}
	}
	var hygiene []recipe.Finding
	err = filepath.WalkDir(w.Dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return nil
		}
		if st.Size() > sizeCapMB<<20 {
			rel, _ := filepath.Rel(w.Dir, p)
			hygiene = append(hygiene, recipe.Finding{Severity: "warning",
				Message: fmt.Sprintf("%s is %d MiB — multi-MB binaries belong in manifests/ (url+sha256) or the local library, not the workspace repo", rel, st.Size()>>20)})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(hygiene) > 0 {
		out["workspace"] = hygiene
	}
	return out, nil
}
