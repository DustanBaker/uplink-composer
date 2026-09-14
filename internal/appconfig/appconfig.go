// Package appconfig persists small per-user UI state — the workspaces the
// app has opened — so the click-to-launch app can reopen the last one and
// offer recents, independent of any terminal working directory.
package appconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

const maxRecent = 8

// Config is the persisted app state.
type Config struct {
	// Recent workspace directories, most-recently-used first.
	Recent []string `json:"recent_workspaces,omitempty"`
	// GuideSeen is set once the first-run guide has been closed, so it is
	// offered once per machine rather than once per window or browser profile.
	GuideSeen bool `json:"guide_seen,omitempty"`

	path string     // where this was loaded from (not serialized)
	mu   sync.Mutex // the portal's handlers save from several goroutines
}

// Dir is the per-user config directory (%AppData%\dsky,
// ~/Library/Application Support/dsky, or ~/.config/dsky).
func Dir() string {
	if base, err := os.UserConfigDir(); err == nil {
		return filepath.Join(base, "dsky")
	}
	return filepath.Join(".", ".dsky")
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

// SetGuideSeen records whether the first-run guide has been closed, and saves.
func (c *Config) SetGuideSeen(seen bool) error {
	c.mu.Lock()
	c.GuideSeen = seen
	c.mu.Unlock()
	return c.Save()
}

// Seen reports whether the first-run guide has been closed.
func (c *Config) Seen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.GuideSeen
}

// AddRecent moves dir to the front of the recents and saves.
func (c *Config) AddRecent(dir string) {
	c.mu.Lock()
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
	c.mu.Unlock()
	_ = c.Save()
}

// Save writes the config, creating the directory as needed.
func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
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
