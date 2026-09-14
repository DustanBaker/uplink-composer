package recipe

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSerialKey(t *testing.T) {
	for in, want := range map[string]string{"5cg1234abc": "5CG1234ABC", " PF3 AB12 ": "PF3AB12", "R90-XY_1.2": "R90-XY_1.2"} {
		if got, err := SerialKey(in); err != nil || got != want {
			t.Errorf("SerialKey(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "Default string", "To be filled by O.E.M.", "System Serial Number", "0", "PC#1"} {
		if _, err := SerialKey(bad); err == nil {
			t.Errorf("SerialKey(%q) accepted", bad)
		}
	}
}

func TestDomainSpecBySerialValidation(t *testing.T) {
	fail := func(f string, a ...any) error { return os.ErrInvalid }
	ok := &DomainSpec{BlobsBySerial: "join-files"}
	if err := ok.validate(fail); err != nil || !ok.Enabled() || ok.Offline() {
		t.Fatalf("plain by-serial: %v enabled=%v offline=%v", err, ok.Enabled(), ok.Offline())
	}
	for _, bad := range []*DomainSpec{
		{BlobsBySerial: "x", Blob: "y"},
		{BlobsBySerial: "x", Join: "corp.example.com", Username: "u", Password: "p"},
		{BlobsBySerial: "x", OU: "OU=PCs,DC=corp"},
	} {
		if err := bad.validate(fail); err == nil {
			t.Errorf("%+v accepted", *bad)
		}
	}
}

// TestDomainSerialScript runs the Setup script under PowerShell with a fake
// serial number and a fake djoin, which is everything but Windows itself.
func TestDomainSerialScript(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("PowerShell (pwsh) is not installed here")
	}
	run := func(t *testing.T, serial string) (log, calls string, dirGone bool) {
		t.Helper()
		root := t.TempDir()
		script := filepath.Join(root, DomainSerialScriptName)
		os.WriteFile(script, []byte(DomainSerialScriptFile()), 0o644)
		dir := filepath.Join(root, DomainSerialDir)
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "5CG1234ABC.txt"), []byte("x"), 0o600)
		os.WriteFile(filepath.Join(dir, "5CG1234ABD.txt"), []byte("x"), 0o600)
		callLog := filepath.Join(root, "calls.txt")
		fake := filepath.Join(root, "djoin.ps1")
		os.WriteFile(fake, []byte("\"$args\" | Add-Content -Path '"+callLog+"'\nexit 0\n"), 0o644)
		logPath := filepath.Join(root, "domain-join.log")
		out, err := exec.Command(pwsh, "-NoProfile", "-NonInteractive", "-File", script,
			"-Serial", serial, "-Dir", dir, "-Djoin", fake, "-WindowsPath", `C:\Windows`, "-Log", logPath).CombinedOutput()
		if err != nil {
			t.Fatalf("script failed: %v\n%s", err, out)
		}
		l, _ := os.ReadFile(logPath)
		c, _ := os.ReadFile(callLog)
		_, statErr := os.Stat(dir)
		return string(l), string(c), os.IsNotExist(statErr)
	}

	t.Run("matching serial joins", func(t *testing.T) {
		log, calls, gone := run(t, " 5cg1234abc ")
		if !strings.Contains(calls, "/requestodj /loadfile") || !strings.Contains(calls, "5CG1234ABC.txt") || !strings.Contains(calls, "/localos") {
			t.Errorf("djoin called with %q", calls)
		}
		if !strings.Contains(log, "join file applied") {
			t.Errorf("log:\n%s", log)
		}
		if !gone {
			t.Error("the join files were left on the computer")
		}
	})
	t.Run("no file for this serial", func(t *testing.T) {
		log, calls, gone := run(t, "NOPE123")
		if calls != "" || !strings.Contains(log, "NOT JOINED: no join file for serial number NOPE123") || !gone {
			t.Errorf("calls %q gone %v log:\n%s", calls, gone, log)
		}
	})
	t.Run("placeholder serial", func(t *testing.T) {
		log, calls, gone := run(t, "Default string")
		if calls != "" || !strings.Contains(log, "placeholder") || !gone {
			t.Errorf("calls %q gone %v log:\n%s", calls, gone, log)
		}
	})
}
