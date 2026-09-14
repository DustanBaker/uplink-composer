package helpers

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// Building Windows install media off Windows used to need two tools this
// program didn't carry: 7-Zip to read the ISO's UDF filesystem (Linux) and
// wimlib to split install.wim across FAT32's 4 GiB limit. DSKY now does both
// itself (internal/udf, internal/wim); the tools are only a fallback for
// images those refuse, and their errors name the install command.

// MissingForWindowsMedia names tools this computer must install before it can
// build Windows media. Nothing, now: DSKY reads ISOs and splits WIMs itself.
func MissingForWindowsMedia(helpersDir string) []string { return nil }

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
