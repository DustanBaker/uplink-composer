// Package appconfig persists small per-user UI state — the workspaces the
// app has opened — so the click-to-launch app can reopen the last one and
// offer recents, independent of any terminal working directory.
package appconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const maxRecent = 8

// Config is the persisted app state.
type Config struct {
	// Recent workspace directories, most-recently-used first.
	Recent []string `json:"recent_workspaces,omitempty"`

	path string // where this was loaded from (not serialized)
}

// Dir is the per-user config directory (%AppData%\uplink,
// ~/Library/Application Support/uplink, or ~/.config/uplink).
func Dir() string {
	if base, err := os.UserConfigDir(); err == nil {
		return filepath.Join(base, "uplink")
	}
	return filepath.Join(".", ".uplink")
}

// Load reads the config, returning an empty one if none exists.
func Load() *Config {
	p := filepath.Join(Dir(), "config.json")
	c := &Config{path: p}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, c)
		c.path = p
	}
	c.prune()
	return c
}

// prune drops recent entries whose directory no longer exists.
func (c *Config) prune() {
	kept := c.Recent[:0]
	seen := map[string]bool{}
	for _, d := range c.Recent {
		if seen[d] {
			continue
		}
		seen[d] = true
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			kept = append(kept, d)
		}
	}
	c.Recent = kept
}

// Current is the most-recent workspace, or "".
func (c *Config) Current() string {
	if len(c.Recent) > 0 {
		return c.Recent[0]
	}
	return ""
}

// AddRecent moves dir to the front of the recents and saves.
func (c *Config) AddRecent(dir string) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	out := []string{abs}
	for _, d := range c.Recent {
		if d != abs && len(out) < maxRecent {
			out = append(out, d)
		}
	}
	c.Recent = out
	_ = c.Save()
}

// Save writes the config, creating the directory as needed.
func (c *Config) Save() error {
	if c.path == "" {
		c.path = filepath.Join(Dir(), "config.json")
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, b, 0o644)
}
