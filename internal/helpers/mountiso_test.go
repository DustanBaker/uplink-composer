package helpers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Windows picks a disk-image provider from the file extension, and the
// library stores every blob under its hash with no extension. Mounting one
// directly failed with "a virtual disk support provider for the specified
// file was not found" before a byte was read, which is what a Windows build
// of a Windows stick hit.
func TestMountableISOGivesWindowsANameItAccepts(t *testing.T) {
	dir := t.TempDir()
	blob := filepath.Join(dir, "768984706b909479417b2368438909440f2967ff05c6a9195ed2667254e465e3")
	if err := os.WriteFile(blob, []byte("pretend this is an ISO"), 0o644); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(dir, "work")

	path, done, err := mountableISO(blob, work)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(filepath.Ext(path), ".iso") {
		t.Errorf("mount path %q does not end in .iso", path)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "pretend this is an ISO" {
		t.Errorf("the mount path does not hold the image: %v %q", err, got)
	}
	// A hard link, not a second copy of a ten-gigabyte file, wherever the
	// filesystem allows it.
	if fi, err := os.Stat(blob); err == nil {
		if lfi, lerr := os.Stat(path); lerr == nil && os.SameFile(fi, lfi) {
			t.Log("linked rather than copied, as intended")
		}
	}
	done()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the temporary mount name was left behind")
	}
	if _, err := os.Stat(blob); err != nil {
		t.Error("the library blob was disturbed")
	}

	// A file that is already named .iso is mounted where it lies.
	named := filepath.Join(dir, "Win11.iso")
	os.WriteFile(named, []byte("x"), 0o644)
	path, done, err = mountableISO(named, work)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if path != named {
		t.Errorf("an .iso was copied needlessly: %q", path)
	}
}
