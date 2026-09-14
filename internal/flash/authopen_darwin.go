package flash

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// openRaw opens a raw disk node for reading and writing. A normal user can't
// (/dev/rdiskN is root:operator 0640), and there is no UAC or pkexec to
// relaunch through on macOS, so writing from the Mac app used to stop with
// "run it with sudo".
//
// Apple's answer is authopen: it shows the standard administrator password
// dialog, opens the one file named, and hands the open descriptor back over a
// socket (-stdoutpipe). DSKY itself never runs as root and never sees the
// password, and the dialog names the disk. Raspberry Pi Imager writes cards
// the same way.
func openRaw(ctx context.Context, path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err == nil || !os.IsPermission(err) {
		return f, err
	}
	return authopen(ctx, path, os.O_RDWR)
}

const authopenPath = "/usr/libexec/authopen"

// errAuthCancelled is a dismissed password dialog.
var errAuthCancelled = errors.New("the administrator password was not given, so the disk was not opened")

func authopen(ctx context.Context, path string, flags int) (*os.File, error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("authopen: socketpair: %w", err)
	}
	parent := fds[0]
	child := os.NewFile(uintptr(fds[1]), "authopen-stdout")
	defer syscall.Close(parent)

	cmd := exec.CommandContext(ctx, authopenPath, "-stdoutpipe", "-o", strconv.Itoa(flags), path)
	cmd.Stdout = child
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		child.Close()
		return nil, fmt.Errorf("authopen: %w", err)
	}
	// Our copy of the child's end is closed, so the read below ends when
	// authopen exits, whether or not it sent anything.
	child.Close()

	var fd = -1
	buf := make([]byte, 64)
	oob := make([]byte, syscall.CmsgSpace(4))
	for fd < 0 {
		n, oobn, _, _, rerr := syscall.Recvmsg(parent, buf, oob, 0)
		if rerr == syscall.EINTR {
			continue
		}
		if rerr != nil || (n == 0 && oobn == 0) {
			break
		}
		if oobn > 0 {
			msgs, perr := syscall.ParseSocketControlMessage(oob[:oobn])
			if perr != nil {
				break
			}
			for _, m := range msgs {
				if rights, perr := syscall.ParseUnixRights(&m); perr == nil && len(rights) > 0 {
					fd = rights[0]
					for _, extra := range rights[1:] {
						syscall.Close(extra)
					}
					break
				}
			}
		}
		if fd < 0 && n == 0 {
			break
		}
	}
	werr := cmd.Wait()
	if fd >= 0 {
		return os.NewFile(uintptr(fd), path), nil
	}
	detail := strings.TrimSpace(stderr.String())
	if werr != nil && detail == "" {
		// authopen exits non-zero with nothing to say when the dialog is
		// cancelled or the password is wrong three times.
		return nil, errAuthCancelled
	}
	return nil, fmt.Errorf("authopen %s: %v %s", path, werr, detail)
}
