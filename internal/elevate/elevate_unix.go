//go:build !windows

package elevate

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// IsElevated reports whether this process runs as root.
func IsElevated() bool { return os.Geteuid() == 0 }

// RunElevated re-runs this executable with args under pkexec (Linux
// desktops); elsewhere it returns an error telling the user the exact sudo
// command. (macOS authopen fd-handoff is the planned upgrade.)
func RunElevated(args []string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return -1, err
	}
	if runtime.GOOS == "linux" {
		if pkexec, err := exec.LookPath("pkexec"); err == nil {
			cmd := exec.Command(pkexec, append([]string{exe}, args...)...)
			cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
			err := cmd.Run()
			if ee, ok := err.(*exec.ExitError); ok {
				return ee.ExitCode(), nil
			}
			if err != nil {
				return -1, err
			}
			return 0, nil
		}
	}
	return -1, fmt.Errorf("elevate: root required — run: sudo %s %s", exe, strings.Join(args, " "))
}

// Hint tells the user how to elevate manually.
func Hint() string { return "re-run with sudo" }
