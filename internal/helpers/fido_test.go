package helpers

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/uplinkresearch/dsky/internal/fetch"
)

// TestFidoPatch applies the off-Windows patch to the pinned Fido when a
// developer machine has it cached, and checks it lands on the pinned hash.
// Moving fidoVersion: fetch the new script, run this, and pin what it logs.
func TestFidoPatch(t *testing.T) {
	if _, err := patchFido([]byte("no such line")); err == nil {
		t.Error("a script without the line was patched anyway")
	}
	home, _ := os.UserHomeDir()
	orig := filepath.Join(home, ".local", "share", "dsky", "helpers", "fido", "Fido-"+fidoVersion+".ps1")
	if sum, err := fetch.SHA256File(orig); err != nil || sum != fidoSHA256 {
		t.Skip("pinned Fido not cached here")
	}
	b, err := os.ReadFile(orig)
	if err != nil {
		t.Fatal(err)
	}
	out, err := patchFido(b)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "patched.ps1")
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}
	sum, _ := fetch.SHA256File(p)
	t.Logf("patched Fido %s sha256 %s", fidoVersion, sum)
	if sum != fidoPatchedSHA256 {
		t.Errorf("patched hash %s, pinned %s", sum, fidoPatchedSHA256)
	}
}

// A version manager's shim that fails when run must not be taken for
// PowerShell: it made downloading Windows fail with the shim's own error.
func TestPowerShellSkipsBrokenShim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses its built-in PowerShell")
	}
	dir := t.TempDir()
	shim := filepath.Join(dir, "shims", "pwsh")
	good := filepath.Join(dir, "real", "pwsh")
	for p, body := range map[string]string{
		shim: "#!/bin/sh\necho 'mise ERROR No version is set for shim: pwsh' >&2\nexit 1\n",
		good: "#!/bin/sh\necho 7\n",
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", dir)
	t.Setenv("DSKY_PWSH", "")
	sep := string(os.PathListSeparator)
	t.Setenv("PATH", filepath.Dir(shim)+sep+filepath.Dir(good)+sep+"/bin")
	got, err := powershellBinary(t.Context())
	if err != nil || got != good {
		t.Fatalf("got %q, %v; want %s", got, err, good)
	}
}
