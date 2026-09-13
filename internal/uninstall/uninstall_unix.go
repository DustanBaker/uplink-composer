//go:build !windows

package uninstall

import (
	"os"
	"path/filepath"
)

// installDir is where install.sh puts the binaries. It is a shared directory
// — plenty of other tools live in ~/.local/bin — so only our own files are
// ever named, and the directory itself is never removed.
func installDir() string {
	if env := os.Getenv("DSKY_BIN"); env != "" {
		return env
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "bin")
	}
	return ""
}

func programItems(self string) []Item {
	var items []Item
	if dir := installDir(); dir != "" {
		for _, f := range []struct{ name, what string }{
			{"dsky", "the dsky program"},
			{"compose", "the compose alias"},
		} {
			p := filepath.Join(dir, f.name)
			items = append(items, Item{Path: p, What: f.what, Kind: KindProgram, Self: sameFile(p, self)})
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return items
	}
	items = append(items,
		Item{Path: filepath.Join(home, ".local", "share", "applications", "dsky.desktop"),
			What: "app-drawer launcher", Kind: KindProgram},
		Item{Path: filepath.Join(home, ".local", "share", "icons", "dsky.png"),
			What: "app icon", Kind: KindProgram},
	)
	return items
}

func sameFile(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	// The alias is a symlink to the real binary; resolve before comparing so
	// running `compose uninstall` still recognises its own image.
	if r, err := filepath.EvalSymlinks(a); err == nil {
		return filepath.Clean(r) == filepath.Clean(b)
	}
	return false
}

// removeFromPath is a no-op: the Unix installer only suggests a PATH entry,
// it never edits a shell profile, so there is nothing of ours to undo.
func removeFromPath(string) error { return nil }

// removeSelf deletes the running binary, which Unix allows: the inode stays
// alive for the running process and the directory entry goes now.
func removeSelf(path string) error { return os.Remove(path) }

// SelfIsDeferred: removal is immediate here.
const SelfIsDeferred = false
