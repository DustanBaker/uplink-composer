package uninstall

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// installDir is where install.ps1 puts the program.
func installDir() string {
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		return filepath.Join(la, "Programs", "uplink")
	}
	return ""
}

func programItems(self string) []Item {
	var items []Item
	dir := installDir()
	if dir == "" {
		return items
	}
	// Named individually rather than removing the directory wholesale, so a
	// copy someone dropped in there is not swept up with ours.
	for _, f := range []struct{ name, what string }{
		{"uplink.exe", "the uplink program"},
		{"compose.exe", "the compose alias"},
		{"uplink-app.exe", "the click-to-launch app"},
		{"uplink.ico", "the app icon"},
	} {
		p := filepath.Join(dir, f.name)
		items = append(items, Item{Path: p, What: f.what, Kind: KindProgram, Self: sameFile(p, self)})
	}
	for _, s := range shortcutPaths() {
		items = append(items, Item{Path: s, What: "shortcut", Kind: KindProgram})
	}
	items = append(items, Item{Path: dir, What: "install folder on your PATH", Kind: KindPath})
	return items
}

func shortcutPaths() []string {
	var out []string
	for _, env := range []string{"APPDATA", "USERPROFILE"} {
		base := os.Getenv(env)
		if base == "" {
			continue
		}
		switch env {
		case "APPDATA":
			out = append(out, filepath.Join(base, "Microsoft", "Windows", "Start Menu", "Programs", "The Uplink CompOSer.lnk"))
		case "USERPROFILE":
			out = append(out, filepath.Join(base, "Desktop", "The Uplink CompOSer.lnk"))
		}
	}
	return out
}

func sameFile(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// removeFromPath drops the install folder from the user PATH, leaving every
// other entry alone. It edits the user variable only — the machine PATH is
// not ours to touch and would need administrator rights anyway.
func removeFromPath(dir string) error {
	cur, err := userPath()
	if err != nil {
		return err
	}
	var kept []string
	found := false
	for _, e := range strings.Split(cur, ";") {
		if e == "" {
			continue
		}
		if sameFile(e, dir) {
			found = true
			continue
		}
		kept = append(kept, e)
	}
	if !found {
		return nil
	}
	// setx truncates at 1024 characters, which would silently eat a long
	// PATH; the .NET call the installer used has no such limit.
	script := fmt.Sprintf(`[Environment]::SetEnvironmentVariable('Path', %s, 'User')`, psQuote(strings.Join(kept, ";")))
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("updating PATH: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func userPath() (string, error) {
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		`[Environment]::GetEnvironmentVariable('Path','User')`).Output()
	if err != nil {
		return "", fmt.Errorf("reading PATH: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// removeSelf deletes the running program. Windows holds the image open for
// as long as the process lives, so a detached helper waits for it to exit,
// deletes the file, and removes the folder if nothing else is left in it.
func removeSelf(path string) error {
	dir := filepath.Dir(path)
	cmd := exec.Command("cmd", "/c",
		fmt.Sprintf(`ping -n 4 127.0.0.1 >nul & del /f /q "%s" & rmdir "%s"`, path, dir))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000008} // DETACHED_PROCESS
	return cmd.Start()
}

// SelfIsDeferred says the running program is removed moments after exit
// rather than immediately, so callers can say so.
const SelfIsDeferred = true
