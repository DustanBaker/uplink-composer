package awake

import (
	"os/exec"
	"sync"
)

// keep runs Apple's own caffeinate for as long as the request lasts. It is a
// child process rather than an IOKit assertion so that a crash here cannot
// leave the machine permanently unable to sleep: the assertion dies with the
// process holding it.
//
//	-i  no idle sleep
//	-m  no disk sleep
//	-s  no system sleep (honoured on AC power only)
func keep(string) func() {
	cmd := exec.Command("caffeinate", "-i", "-m", "-s")
	if err := cmd.Start(); err != nil {
		return func() {} // no caffeinate: write anyway
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		})
	}
}
