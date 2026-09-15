// Package testpwsh finds a PowerShell that can actually run, for tests that
// check DSKY's generated scripts against the real parser.
//
// Looking it up on PATH is not enough: a mise shim with no version selected
// sits there and fails on every call, which turns "this developer has not
// selected a PowerShell" into a failing test about scripts.
package testpwsh

import "os/exec"

// Find returns a working pwsh, or "" when there is none here.
func Find() string {
	p, err := exec.LookPath("pwsh")
	if err != nil {
		return ""
	}
	if err := exec.Command(p, "-NoProfile", "-NonInteractive", "-Command", "exit 0").Run(); err != nil {
		return ""
	}
	return p
}
