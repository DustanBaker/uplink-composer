package flash

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestAuthopenWritesDisk writes a disposable disk (DSKY_TEST_DISK, a RAM disk
// from hdiutil in the mac-window workflow) through authopen and reads it back.
//
// As a normal user that brings up the password dialog, which nobody can answer
// on a CI runner, and macOS no longer lets even root change the rule to skip
// it. So CI runs this as root and calls authopen directly: authopen's rule lets
// root through without a dialog, and everything else, the descriptor handed
// back over the socket and the write through it, is what a person at a Mac
// gets once they type their password. As a normal user it checks that the
// plain open is refused, then goes through openRaw, dialog and all.
func TestAuthopenWritesDisk(t *testing.T) {
	disk := os.Getenv("DSKY_TEST_DISK")
	if disk == "" {
		t.Skip("DSKY_TEST_DISK not set")
	}
	raw := strings.Replace(disk, "/dev/disk", "/dev/rdisk", 1)
	var f *os.File
	var err error
	if os.Geteuid() == 0 {
		f, err = authopen(context.Background(), raw, os.O_RDWR)
	} else {
		if g, oerr := os.OpenFile(raw, os.O_RDWR, 0); oerr == nil {
			g.Close()
			t.Fatalf("%s opened without authopen, so this run proves nothing", raw)
		} else if !os.IsPermission(oerr) {
			t.Fatal(oerr)
		}
		f, err = openRaw(context.Background(), raw)
	}
	if err != nil {
		t.Fatalf("opening through authopen: %v", err)
	}
	pattern := bytes.Repeat([]byte("DSKY"), 1<<18) // 1 MiB, sector-aligned
	if _, err := f.WriteAt(pattern, 0); err != nil {
		t.Fatalf("write through the authopen descriptor: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Logf("sync: %v", err)
	}
	f.Close()
	out, err := exec.Command("dd", "if="+raw, "bs=1048576", "count=1").Output()
	if os.Geteuid() != 0 {
		out, err = exec.Command("sudo", "dd", "if="+raw, "bs=1048576", "count=1").Output()
	}
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if !bytes.Equal(out, pattern) {
		t.Fatalf("read back %d bytes that differ from what was written", len(out))
	}
}
