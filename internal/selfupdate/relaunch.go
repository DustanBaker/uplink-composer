package selfupdate

import (
	"os"
	"os/exec"
	"path/filepath"
)

// RestartedEnv marks a process that was started by Relaunch rather than by a
// person. The new build uses it to wait for its predecessor to let go of the
// port before deciding whether a portal is already running — without that it
// finds the outgoing process still listening, concludes one is running, and
// hands the window back to the version it just replaced.
const RestartedEnv = "DSKY_RESTARTED"

// launchedAs is this program's path as it was when the process started.
//
// It has to be read before Apply runs. Apply renames the running file to
// dsky.old and deletes it, and on Linux os.Executable follows the running file
// through /proc/self/exe — so asked afterwards it answers ".../dsky.old", which
// no longer exists, and the relaunch failed with nothing reopening. macOS and
// Windows report the path the program was started from, which is why updates
// restarted there and not on Linux.
var launchedAs, _ = os.Executable()

// Relaunch starts the newly installed build.
//
// Apply renames the running executable aside and puts the new one at the same
// path, so this starts the new version rather than the one asking for it. The
// child is deliberately not waited on: the caller's next act is to exit, and
// the child outlives it.
func Relaunch(args ...string) error {
	exe := launchedAs
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return err
		}
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = filepath.Dir(exe)
	cmd.Env = append(os.Environ(), RestartedEnv+"=1")
	return cmd.Start()
}

// WasRestarted reports whether this process was started by Relaunch.
func WasRestarted() bool { return os.Getenv(RestartedEnv) != "" }
