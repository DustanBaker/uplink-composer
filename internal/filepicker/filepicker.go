// Package filepicker opens the host's own folder-chooser dialog.
//
// A browser cannot hand a server an absolute path — a file input yields file
// contents and relative names, not a location on disk — so a loopback app
// that needs one has to ask the desktop directly. That works here precisely
// because the server is on the same machine as the person using it.
//
// Every platform shells out to the chooser its desktop already ships, so
// nothing is bundled and nothing is drawn by us. On a headless host there is
// no chooser and Pick returns ErrUnavailable, which callers should treat as
// "keep letting them type the path" rather than as a failure.
package filepicker

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// ErrUnavailable means this host has no usable folder chooser (headless, or
// no supported dialog tool installed).
var ErrUnavailable = errors.New("no folder chooser available on this host")

// ErrCancelled means the dialog opened and the person dismissed it.
var ErrCancelled = errors.New("folder selection cancelled")

// PickFolder opens a native folder chooser and returns the absolute path
// chosen. It blocks until the person answers, so give it a generous context.
func PickFolder(ctx context.Context, title string) (string, error) {
	if title == "" {
		title = "Select a folder"
	}
	return pickFolder(ctx, title)
}

// ImageExts and InstallerExts are what the choosers offer to filter on. The
// filter is a convenience only: every chooser here also allows any file,
// because refusing to show a file with an unusual name would be worse than
// showing too much.
var (
	ImageExts     = []string{"iso", "img", "raw", "bin", "wic", "xz", "zst", "gz", "bz2"}
	InstallerExts = []string{"msi", "exe"}
)

// PickFile opens a native file chooser and returns the absolute path chosen.
// label names the filter in the dialog; exts is what it matches. Same blocking
// contract as PickFolder.
func PickFile(ctx context.Context, title, label string, exts []string) (string, error) {
	if title == "" {
		title = "Select a file"
	}
	if label == "" {
		label = "Files"
	}
	return pickFile(ctx, title, label, exts)
}

// PickImage opens a chooser for a disk image.
func PickImage(ctx context.Context, title string) (string, error) {
	if title == "" {
		title = "Select a disk image"
	}
	return PickFile(ctx, title, "Disk images", ImageExts)
}

// PickInstaller opens a chooser for a Windows installer.
func PickInstaller(ctx context.Context, title string) (string, error) {
	if title == "" {
		title = "Select an installer"
	}
	return PickFile(ctx, title, "Installers", InstallerExts)
}

// run executes a chooser and tidies its output. An empty result is a
// cancellation: every chooser here prints the path only on OK.
func run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	path := strings.TrimSpace(string(out))
	if err != nil {
		// zenity and kdialog exit non-zero on cancel, which is not a fault.
		if path == "" {
			return "", ErrCancelled
		}
		return "", err
	}
	if path == "" {
		return "", ErrCancelled
	}
	return path, nil
}
