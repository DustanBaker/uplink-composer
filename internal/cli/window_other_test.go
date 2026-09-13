//go:build linux

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The launcher rewrite touches a file in the user's home directory, so it has
// to be exactly as narrow as it claims: this binary with `serve --open`, once,
// and nothing somebody wrote themselves.
func TestUpgradeLauncher(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".local", "share", "applications")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "dsky.desktop")
	write := func(execLine string) {
		body := "[Desktop Entry]\nName=DSKY\nExec=" + execLine + "\nIcon=x\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func() string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	write(exe + " serve --open")
	if !upgradeLauncher() {
		t.Fatal("the v0.7.0 launcher was not upgraded")
	}
	if got := read(); !strings.Contains(got, "Exec="+exe+" app\n") || !strings.Contains(got, "Icon=x\n") {
		t.Fatalf("rewritten launcher is wrong:\n%s", got)
	}
	if upgradeLauncher() {
		t.Fatal("an already-upgraded launcher was reported as changed")
	}

	for _, own := range []string{exe + " serve --open --port 9000", "/somewhere/else/dsky serve --open"} {
		write(own)
		if upgradeLauncher() || !strings.Contains(read(), "Exec="+own+"\n") {
			t.Fatalf("a launcher somebody edited (%q) was touched", own)
		}
	}
}
