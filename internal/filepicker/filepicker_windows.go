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

// pickFile drives the Windows shell's file browser. Same -STA requirement as
// the folder chooser, and the same always-on-top owner so the dialog cannot
// open behind the browser.
func pickFile(ctx context.Context, title, label string, exts []string) (string, error) {
	if os.Getenv("SESSIONNAME") == "" && os.Getenv("USERNAME") == "" {
		return "", ErrUnavailable
	}
	pats := make([]string, 0, len(exts))
	for _, e := range exts {
		pats = append(pats, "*."+e)
	}
	filter := label + "|" + strings.Join(pats, ";") + "|All files|*.*"
	script := `
Add-Type -AssemblyName System.Windows.Forms
$dlg = New-Object System.Windows.Forms.OpenFileDialog
$dlg.Title = '` + psQuote(title) + `'
$dlg.Filter = '` + psQuote(filter) + `'
$dlg.CheckFileExists = $true
$dlg.Multiselect = $false
$owner = New-Object System.Windows.Forms.Form
$owner.TopMost = $true
if ($dlg.ShowDialog($owner) -eq [System.Windows.Forms.DialogResult]::OK) {
  [Console]::Out.Write($dlg.FileName)
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
