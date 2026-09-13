package awake

import (
	"os/exec"
	"sync"
)

// keep takes a systemd inhibitor lock for the life of a child process. The
// lock is held by that child, so it is released when the child dies —
// including if this program is killed, which a D-Bus lock held in-process
// would also do but with more machinery.
//
// Not every Linux runs systemd, and this tool is often run over SSH where
// there is no session to inhibit at all. Both are ordinary, so a missing
// systemd-inhibit is a no-op rather than an error.
func keep(reason string) func() {
	if _, err := exec.LookPath("systemd-inhibit"); err != nil {
		return func() {}
	}
	if reason == "" {
		reason = "writing removable media"
	}
	cmd := exec.Command("systemd-inhibit",
		"--what=sleep:idle", "--mode=block",
		"--who=bootwright", "--why="+reason,
		"sleep", "infinity")
	if err := cmd.Start(); err != nil {
		return func() {}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		})
	}
}
