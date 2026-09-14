package helpers

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// Building Windows install media off Windows needs two tools this program
// doesn't carry: something that reads the ISO's UDF filesystem (7-Zip on
// Linux; macOS has hdiutil built in) and wimlib, which splits install.wim
// across the 4 GiB FAT32 file limit. Missing either used to surface only when
// the build reached it — after the Windows download — as a message naming a
// helpers folder rather than the one command that fixes it.

// MissingForWindowsMedia names the tools this computer lacks to build Windows
// install media. Empty on Windows, which has what it needs built in.
func MissingForWindowsMedia(helpersDir string) []string {
	var missing []string
	switch runtime.GOOS {
	case "windows":
		return nil
	case "darwin":
	default:
		if _, err := Find7z(helpersDir); err != nil {
			missing = append(missing, "7-Zip")
		}
	}
	if _, err := FindWimlib(helpersDir); err != nil {
		missing = append(missing, "wimlib")
	}
	return missing
}

// InstallCommand is the command that installs the named tools on this
// computer, as far as it can tell which package manager that is.
func InstallCommand(missing []string) string {
	if len(missing) == 0 {
		return ""
	}
	has := func(name string) bool {
		for _, m := range missing {
			if m == name {
				return true
			}
		}
		return false
	}
	if runtime.GOOS == "darwin" {
		return "brew install wimlib"
	}
	type pkgs struct{ cmd, sevenZip, wimlib string }
	var p pkgs
	switch distroFamily() {
	case "arch":
		p = pkgs{"sudo pacman -S", "7zip", "wimlib"}
	case "debian":
		p = pkgs{"sudo apt install", "7zip", "wimtools"}
	case "fedora":
		p = pkgs{"sudo dnf install", "7zip", "wimlib-utils"}
	case "suse":
		p = pkgs{"sudo zypper install", "7zip", "wimtools"}
	default:
		return "install " + strings.Join(missing, " and ") + " with your package manager"
	}
	var names []string
	if has("7-Zip") {
		names = append(names, p.sevenZip)
	}
	if has("wimlib") {
		names = append(names, p.wimlib)
	}
	return p.cmd + " " + strings.Join(names, " ")
}

// WindowsMediaToolsError is the error a Windows build returns before it starts
// when tools are missing, or nil.
func WindowsMediaToolsError(helpersDir string) error {
	missing := MissingForWindowsMedia(helpersDir)
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("building Windows media on this computer needs %s, which %s not installed — run: %s",
		strings.Join(missing, " and "), map[bool]string{true: "is", false: "are"}[len(missing) == 1], InstallCommand(missing))
}

// distroFamily reads /etc/os-release: arch, debian, fedora, suse or "".
func distroFamily() string {
	f, err := os.Open(osReleasePath)
	if err != nil {
		return ""
	}
	defer f.Close()
	var ids []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		for _, key := range []string{"ID=", "ID_LIKE="} {
			if v, ok := strings.CutPrefix(line, key); ok {
				ids = append(ids, strings.Fields(strings.Trim(v, `"'`))...)
			}
		}
	}
	for _, id := range ids {
		switch id {
		case "arch", "archlinux", "manjaro", "endeavouros", "cachyos", "omarchy", "garuda":
			return "arch"
		case "debian", "ubuntu", "linuxmint", "pop":
			return "debian"
		case "fedora", "rhel", "centos", "rocky", "almalinux", "nobara":
			return "fedora"
		case "opensuse", "suse", "opensuse-tumbleweed", "opensuse-leap", "sles":
			return "suse"
		}
	}
	return ""
}

var osReleasePath = "/etc/os-release"
