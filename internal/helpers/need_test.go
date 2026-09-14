package helpers

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstallCommand(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /etc/os-release")
	}
	dir := t.TempDir()
	old := osReleasePath
	defer func() { osReleasePath = old }()
	for release, want := range map[string]string{
		"ID=omarchy\nID_LIKE=arch\n":  "sudo pacman -S 7zip wimlib",
		"ID=ubuntu\nID_LIKE=debian\n": "sudo apt install 7zip wimtools",
		"ID=fedora\n":                 "sudo dnf install 7zip wimlib-utils",
		"ID=\"opensuse-tumbleweed\"\nID_LIKE=\"opensuse suse\"\n": "sudo zypper install 7zip wimtools",
		"ID=gentoo\n": "install 7-Zip and wimlib with your package manager",
	} {
		osReleasePath = filepath.Join(dir, "os-release")
		os.WriteFile(osReleasePath, []byte(release), 0o644)
		if got := InstallCommand([]string{"7-Zip", "wimlib"}); got != want {
			t.Errorf("%q: got %q, want %q", release, got, want)
		}
	}
	osReleasePath = filepath.Join(dir, "os-release")
	os.WriteFile(osReleasePath, []byte("ID=arch\n"), 0o644)
	if got := InstallCommand([]string{"wimlib"}); got != "sudo pacman -S wimlib" {
		t.Errorf("only wimlib missing: %q", got)
	}
}

// With neither 7-Zip nor wimlib on the machine, a Windows build is not
// blocked: DSKY reads the ISO and splits the WIM itself.
func TestWindowsMediaNeedsNoTools(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("DSKY_7Z", "")
	t.Setenv("DSKY_WIMLIB", "")
	if err := WindowsMediaToolsError(t.TempDir()); err != nil {
		t.Fatalf("err = %v", err)
	}
}
