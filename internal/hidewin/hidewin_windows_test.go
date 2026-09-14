package hidewin

import (
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

func TestCmdHidesAndKeepsFlags(t *testing.T) {
	c := exec.Command("cmd", "/c", "exit")
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008}
	Cmd(c)
	if !c.SysProcAttr.HideWindow || c.SysProcAttr.CreationFlags != 0x08000008 {
		t.Fatalf("SysProcAttr = %+v", c.SysProcAttr)
	}
}

// Hiding the window must not cost the output: DSKY reads PowerShell's and
// diskpart's answers through pipes.
func TestHiddenCommandStillReturnsOutput(t *testing.T) {
	out, err := Cmd(exec.Command("cmd", "/c", "echo", "hidden-ok")).Output()
	if err != nil || !strings.Contains(string(out), "hidden-ok") {
		t.Fatalf("output %q, err %v", out, err)
	}
}
