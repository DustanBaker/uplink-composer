package filepicker

import (
	"context"
	"strings"
)

// pickFolder uses AppleScript's standard folder chooser. "choose folder"
// yields an alias, so it is converted to a POSIX path before returning.
func pickFolder(ctx context.Context, title string) (string, error) {
	script := `POSIX path of (choose folder with prompt "` + osaQuote(title) + `")`
	path, err := run(ctx, "osascript", "-e", script)
	if err != nil {
		return "", err
	}
	// A chosen folder comes back with a trailing separator; callers want a
	// plain directory path.
	return strings.TrimSuffix(path, "/"), nil
}

func osaQuote(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`)
}
