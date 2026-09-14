// Package hidewin keeps the console programs DSKY runs on Windows (PowerShell,
// diskpart, expand) from opening a window of their own.
//
// The windowed app has no console, so Windows gives every console program it
// starts a new, visible one: a PowerShell window flashing up while an ISO is
// fetched or a stick is written, which looks like something going wrong and
// can be closed by accident, killing the step. Output still reaches DSKY
// through pipes; only the window is suppressed. Elsewhere this does nothing.
package hidewin

import "os/exec"

// Cmd marks c to run without a console window and returns it.
func Cmd(c *exec.Cmd) *exec.Cmd {
	hide(c)
	return c
}
