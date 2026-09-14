//go:build !windows

package hidewin

import "os/exec"

func hide(*exec.Cmd) {}
