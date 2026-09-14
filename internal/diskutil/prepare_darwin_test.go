package diskutil

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/uplinkresearch/dsky/internal/device"
)

// TestPrepareWithoutRoot erases a disposable disk (DSKY_TEST_DISK2) with
// diskutil as this user: Erase and prepare on a Mac runs in the app's own
// process, with no password, so it has to work without root.
func TestPrepareWithoutRoot(t *testing.T) {
	disk := os.Getenv("DSKY_TEST_DISK2")
	if disk == "" {
		t.Skip("DSKY_TEST_DISK2 not set")
	}
	if os.Geteuid() == 0 {
		t.Fatal("running as root proves nothing")
	}
	dev := device.Device{ID: strings.Replace(disk, "/dev/disk", "/dev/rdisk", 1)}
	err := prepare(context.Background(), dev, Options{Scheme: GPT, FS: ExFAT, Label: "DSKYTEST"}, func(s string) { t.Log(s) })
	if err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command("diskutil", "list", disk).CombinedOutput()
	if !strings.Contains(string(out), "DSKYTEST") {
		t.Fatalf("no DSKYTEST volume after erasing:\n%s", out)
	}
}
