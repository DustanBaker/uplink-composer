package agent

import (
	"os"
	"path/filepath"
	"strings"
)

// No desktop shortcuts, ever.
//
// Installers scatter them: Chrome, Reader, Zoom, Teams and every vendor
// utility drop an icon on the desktop, and some drop one on the Default
// profile's desktop so that every account made later gets it too. A machine
// handed over by DSKY has a clean desktop, whatever the installers wanted.
//
// This is a sweep rather than a set of switches. Every installer spells "no
// shortcut please" differently, many ignore it, and winget passes nothing
// through reliably -- so the shortcuts are taken off afterwards, which works
// the same for a winget package, an operator's own MSI and a driver bundle's
// leftovers.

const stepDesktop = "desktop"

// shortcutExt is what counts as a shortcut. Anything else on the desktop was
// put there by a person and is left alone.
func shortcutExt(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".lnk", ".url", ".website":
		return true
	}
	return false
}

// removeShortcuts clears every shortcut out of dirs, and says what it could
// not remove. A directory that is not there is not a problem: not every
// machine has every profile.
func removeShortcuts(dirs []string) (removed int, stuck []string) {
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !shortcutExt(e.Name()) {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if err := os.Remove(p); err != nil {
				stuck = append(stuck, e.Name())
				continue
			}
			removed++
		}
	}
	return removed, stuck
}

// desktopDirsFn finds the desktops to sweep, as a variable so the package's
// tests sweep their own temporary folders. The real one, run by `go test` on a
// Windows machine, would delete that developer's own shortcuts.
var desktopDirsFn = desktopDirs

// tidyDesktop is run after the programs go in, and again at the end of the
// whole run: an install that finishes in the signed-in user's session can put
// an icon there after the step that started it has been recorded as done.
func (a *Agent) tidyDesktop() {
	dirs := desktopDirsFn()
	if len(dirs) == 0 {
		return
	}
	removed, stuck := removeShortcuts(dirs)
	if removed > 0 {
		a.J.Info(stepDesktop, "removed %d desktop shortcut(s)", removed)
	}
	if len(stuck) > 0 {
		a.J.Fail(stepDesktop, "could not remove %d desktop shortcut(s): %s",
			len(stuck), strings.Join(stuck, ", "))
	}
}
