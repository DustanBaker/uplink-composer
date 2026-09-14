package flash

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestAuthopenWritesDisk writes a disk this user cannot open, through
// authopen, and reads it back as root. It needs a disposable disk
// (DSKY_TEST_DISK, e.g. a RAM disk from hdiutil) and an authorization rule
// that lets authopen through without a dialog, which the mac-window workflow
// sets up; it never runs anywhere else.
func TestAuthopenWritesDisk(t *testing.T) {
	disk := os.Getenv("DSKY_TEST_DISK")
	if disk == "" {
		t.Skip("DSKY_TEST_DISK not set")
	}
	raw := strings.Replace(disk, "/dev/disk", "/dev/rdisk", 1)
	if f, err := os.OpenFile(raw, os.O_RDWR, 0); err == nil {
		f.Close()
		t.Fatalf("%s opened without authopen, so this run proves nothing", raw)
	} else if !os.IsPermission(err) {
		t.Fatal(err)
	}
	f, err := openRaw(context.Background(), raw)
	if err != nil {
		t.Fatalf("openRaw: %v", err)
	}
	pattern := bytes.Repeat([]byte("DSKY"), 1<<18) // 1 MiB, sector-aligned
	if _, err := f.WriteAt(pattern, 0); err != nil {
		t.Fatalf("write through the authopen descriptor: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Logf("sync: %v", err)
	}
	f.Close()
	out, err := exec.Command("sudo", "dd", "if="+raw, "bs=1048576", "count=1").Output()
	if err != nil {
		t.Fatalf("reading back as root: %v", err)
	}
	if !bytes.Equal(out, pattern) {
		t.Fatalf("read back %d bytes that differ from what was written", len(out))
	}
}
