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

// pickFile uses AppleScript's file chooser. No type filter: "choose file of
// type" takes UTIs and extensions inconsistently across versions, and a
// filter that hid the file someone wanted would be worse than none.
func pickFile(ctx context.Context, title, _ string, _ []string) (string, error) {
	script := `POSIX path of (choose file with prompt "` + osaQuote(title) + `" without invisibles)`
	return run(ctx, "osascript", "-e", script)
}

func osaQuote(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`)
}
