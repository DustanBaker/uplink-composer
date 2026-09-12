package filepicker

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// pickFolder tries the GTK and KDE choosers in turn. With no display there is
// nothing to show a dialog on, which is a normal state for this tool (it runs
// fine over SSH), so that reports ErrUnavailable rather than an error.
func pickFolder(ctx context.Context, title string) (string, error) {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return "", ErrUnavailable
	}
	if path, err := exec.LookPath("zenity"); err == nil {
		return run(ctx, path, "--file-selection", "--directory", "--title="+title)
	}
	if path, err := exec.LookPath("kdialog"); err == nil {
		return run(ctx, path, "--getexistingdirectory", ".", "--title", title)
	}
	return "", ErrUnavailable
}

// pickFile tries the same two choosers in file mode. Both filters also list
// every file, so a file with an unusual name stays reachable.
func pickFile(ctx context.Context, title, label string, exts []string) (string, error) {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return "", ErrUnavailable
	}
	pats := make([]string, 0, len(exts))
	for _, e := range exts {
		pats = append(pats, "*."+e)
	}
	if path, err := exec.LookPath("zenity"); err == nil {
		return run(ctx, path, "--file-selection", "--title="+title,
			"--file-filter="+label+" | "+strings.Join(pats, " "),
			"--file-filter=All files | *")
	}
	if path, err := exec.LookPath("kdialog"); err == nil {
		return run(ctx, path, "--getopenfilename", ".",
			strings.Join(pats, " ")+"|"+label+"\n*|All files", "--title", title)
	}
	return "", ErrUnavailable
}
