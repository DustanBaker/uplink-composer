package filepicker

import (
	"context"
	"os"
	"strings"
)

// pickFolder drives the Windows shell's own folder browser through
// PowerShell. Windows Forms requires a single-threaded apartment, hence -STA;
// without it ShowDialog returns immediately and silently picks nothing.
func pickFolder(ctx context.Context, title string) (string, error) {
	if os.Getenv("SESSIONNAME") == "" && os.Getenv("USERNAME") == "" {
		return "", ErrUnavailable
	}
	script := `
Add-Type -AssemblyName System.Windows.Forms
$dlg = New-Object System.Windows.Forms.FolderBrowserDialog
$dlg.Description = '` + psQuote(title) + `'
$dlg.ShowNewFolderButton = $false
# An always-on-top owner, otherwise the dialog can open behind the browser
# the person just clicked in and look like nothing happened.
$owner = New-Object System.Windows.Forms.Form
$owner.TopMost = $true
if ($dlg.ShowDialog($owner) -eq [System.Windows.Forms.DialogResult]::OK) {
  [Console]::Out.Write($dlg.SelectedPath)
}
$owner.Dispose()
`
	// Deliberately not -NonInteractive: the whole point is to interact.
	return run(ctx, "powershell", "-NoProfile", "-STA", "-Command", script)
}

// psQuote escapes a value for a PowerShell single-quoted string.
func psQuote(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
