//go:build !windows && !darwin

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/uplinkresearch/dsky/internal/appconfig"
)

// The portal in its own window, on Linux.
//
// Linux's web views (WebKitGTK and friends) are C libraries that differ by
// distribution and are often not installed, and every Go binding for them
// needs a C compiler. What these machines almost always have instead is a
// Chromium-family browser, and Chromium has an app mode: one
// window, no tabs, no address bar, the page's own title in the title bar. It
// is a subprocess, so nothing is linked.
//
// The window gets a profile of its own under the config directory. That is
// not tidiness. A browser started against a profile that is already open
// hands the URL to the running copy and exits at once, so starting it against
// somebody's everyday profile would return immediately — and the caller reads
// "showWindow returned" as "the window was closed" and stops the server. A
// profile only this window uses makes the process's lifetime the window's.
//
// No Chromium-family browser means no window, and the caller opens the portal
// in the default browser as before.
func showWindow(url, title string) bool {
	bin := appBrowser()
	if bin == "" {
		return false
	}
	profile := filepath.Join(appconfig.Dir(), "window")
	args := []string{
		"--app=" + url,
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--window-size=1280,860",
	}
	if runtime.GOOS == "linux" {
		// X11's WM_CLASS, so a window manager can tell this window from the
		// browser it is borrowed from. Wayland ignores it harmlessly.
		args = append(args, "--class="+title)
	}
	seedProfile(profile, bin)
	cmd := exec.Command(bin, args...)
	if err := cmd.Start(); err != nil {
		return false
	}
	setWindowCloser(func() { stopProfileBrowser(profile, cmd) })
	defer setWindowCloser(nil)

	_ = cmd.Wait()
	// Normally the window has closed by now. But if this profile's browser
	// was somehow already running — a window left behind by a portal that
	// died — the process above passed the URL along and exited, and the
	// window lives in that other process. Wait on whichever process holds
	// the profile, so closing the window is still what quits.
	for pid := profileOwner(profile); pid > 0 && alive(pid); pid = profileOwner(profile) {
		time.Sleep(time.Second)
	}
	return true
}

// seedProfile prepares a profile that has never been opened.
//
// Brave greets a new profile with a banner about its usage analytics across
// the top of the window, which in an app window is the first thing anybody
// sees, above the app. Recording the notice as seen, with the analytics off,
// is the answer a person would give, in a profile that only ever shows DSKY.
// An existing profile is left exactly as its owner left it.
func seedProfile(profile, bin string) {
	if _, err := os.Stat(profile); err == nil {
		return
	}
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return
	}
	if strings.Contains(strings.ToLower(filepath.Base(bin)), "brave") {
		_ = os.WriteFile(filepath.Join(profile, "Local State"),
			[]byte(`{"brave":{"p3a":{"enabled":false,"notice_acknowledged":true}}}`), 0o600)
	}
}

// focusWindow cannot raise another process's window without a desktop-specific
// tool, so a second launch opens a second window onto the same portal instead.
func focusWindow(title string) bool { return false }

// appBrowser finds a Chromium-family browser, preferring the one somebody has
// chosen as their default, so the window looks like the browser they use.
func appBrowser() string {
	// The default browser's desktop id names the family even when the
	// executable is spelled differently from distribution to distribution.
	families := []struct {
		id   string
		bins []string
	}{
		{"brave", []string{"brave", "brave-browser", "brave-browser-stable"}},
		{"google-chrome", []string{"google-chrome-stable", "google-chrome"}},
		{"chromium", []string{"chromium", "chromium-browser"}},
		{"microsoft-edge", []string{"microsoft-edge-stable", "microsoft-edge"}},
		{"vivaldi", []string{"vivaldi-stable", "vivaldi"}},
	}
	var order [][]string
	if out, err := exec.Command("xdg-settings", "get", "default-web-browser").Output(); err == nil {
		def := strings.ToLower(string(out))
		for _, f := range families {
			if strings.Contains(def, f.id) {
				order = append(order, f.bins)
			}
		}
	}
	for _, f := range families {
		order = append(order, f.bins)
	}
	for _, bins := range order {
		for _, b := range bins {
			if p, err := exec.LookPath(b); err == nil {
				return p
			}
		}
	}
	return ""
}

// profileOwner is the pid holding a Chromium profile open, read from the
// SingletonLock symlink Chromium keeps in it ("hostname-pid"), or 0.
func profileOwner(profile string) int {
	target, err := os.Readlink(filepath.Join(profile, "SingletonLock"))
	if err != nil {
		return 0
	}
	i := strings.LastIndex(target, "-")
	if i < 0 {
		return 0
	}
	pid, err := strconv.Atoi(target[i+1:])
	if err != nil {
		return 0
	}
	return pid
}

func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	return err == nil && p.Signal(syscall.Signal(0)) == nil
}

// stopProfileBrowser closes the window from outside, for the updater: the
// process we started, and whichever process actually owns the profile.
func stopProfileBrowser(profile string, cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
	if pid := profileOwner(profile); pid > 0 {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Signal(syscall.SIGTERM)
		}
	}
}

// upgradeLauncher points an install.sh app-drawer launcher that still runs
// `serve --open` at `app` instead, reporting whether it changed one. Only a
// launcher whose command is exactly this binary with that one flag is
// touched: anything else is somebody's own edit.
func upgradeLauncher() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	path := filepath.Join(home, ".local", "share", "applications", "dsky.desktop")
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	old := "Exec=" + exe + " serve --open\n"
	if !strings.Contains(string(b), old) {
		return false
	}
	updated := strings.Replace(string(b), old, "Exec="+exe+" app\n", 1)
	if os.WriteFile(path, []byte(updated), 0o644) != nil {
		return false
	}
	return true
}
