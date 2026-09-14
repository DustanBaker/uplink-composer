package compose

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Runs the answer file's copy command through cmd.exe as Setup would, against
// a folder mapped to a drive letter like a USB stick, into a temporary
// "C:\Windows". Only the target is swapped; the loop, the checks and xcopy
// are the command Setup runs.
func TestOEMCopyCommandRunsOnWindows(t *testing.T) {
	media := t.TempDir()
	scripts := filepath.Join(media, `sources\$OEM$\$$\Setup\Scripts`)
	os.MkdirAll(filepath.Join(scripts, `Drivers\pack`), 0o755)
	os.WriteFile(filepath.Join(scripts, "firstboot.cmd"), []byte("@echo first boot"), 0o644)
	os.WriteFile(filepath.Join(scripts, `Drivers\pack\a.inf`), []byte("inf"), 0o644)

	letter := ""
	for c := 'Z'; c >= 'M'; c-- {
		if _, err := os.Stat(string(c) + `:\`); err != nil {
			letter = string(c) + ":"
			break
		}
	}
	if letter == "" {
		t.Skip("no free drive letter")
	}
	if out, err := exec.Command("subst", letter, media).CombinedOutput(); err != nil {
		t.Skipf("subst: %v %s", err, out)
	}
	defer exec.Command("subst", letter, "/d").Run()

	target := filepath.Join(t.TempDir(), "Windows")
	run := func() {
		t.Helper()
		line := strings.ReplaceAll(oemCopyCommand, `C:\Windows`, target)
		c := exec.Command(`C:\Windows\System32\cmd.exe`)
		c.SysProcAttr = &syscall.SysProcAttr{CmdLine: line}
		out, err := c.CombinedOutput()
		t.Logf("%s", out)
		if err != nil {
			t.Fatalf("command failed: %v", err)
		}
	}

	run()
	for _, rel := range []string{`Setup\Scripts\firstboot.cmd`, `Setup\Scripts\Drivers\pack\a.inf`} {
		if _, err := os.Stat(filepath.Join(target, rel)); err != nil {
			t.Errorf("%s not copied: %v", rel, err)
		}
	}

	// Setup already copied: nothing is overwritten.
	os.WriteFile(filepath.Join(target, `Setup\Scripts\firstboot.cmd`), []byte("from Setup"), 0o644)
	run()
	if b, _ := os.ReadFile(filepath.Join(target, `Setup\Scripts\firstboot.cmd`)); string(b) != "from Setup" {
		t.Errorf("copied over what Setup put there: %q", b)
	}
}
