// Package appcatalog is the built-in list of programs Quick Install can add to
// a machine. Each entry names the package in the platform's own package
// manager — winget on Windows, apt on Debian/Ubuntu — so installers come from
// the vendor at first boot and nothing large rides on the media.
package appcatalog

import "strings"

// App is one installable program.
type App struct {
	ID       string // short picker id, e.g. "chrome"
	Name     string
	Category string
	Winget   string // winget package id; empty = not available on Windows
	Apt      string // apt package name; empty = not available via apt
	Notes    string
}

// builtin is the shipped list, ordered by category then name — the order the
// picker shows.
var builtin = []App{
	{ID: "chrome", Name: "Google Chrome", Category: "Browsers", Winget: "Google.Chrome", Notes: "Machine-wide install"},
	{ID: "firefox", Name: "Mozilla Firefox", Category: "Browsers", Winget: "Mozilla.Firefox", Apt: "firefox"},

	{ID: "adobereader", Name: "Adobe Acrobat Reader", Category: "Documents", Winget: "Adobe.Acrobat.Reader.64-bit"},
	{ID: "libreoffice", Name: "LibreOffice", Category: "Documents", Winget: "TheDocumentFoundation.LibreOffice", Apt: "libreoffice"},
	{ID: "onlyoffice", Name: "ONLYOFFICE Desktop Editors", Category: "Documents", Winget: "ONLYOFFICE.DesktopEditors"},

	{ID: "vlc", Name: "VLC media player", Category: "Media", Winget: "VideoLAN.VLC", Apt: "vlc"},
	{ID: "gimp", Name: "GIMP", Category: "Media", Winget: "GIMP.GIMP", Apt: "gimp"},

	{ID: "7zip", Name: "7-Zip", Category: "Utilities", Winget: "7zip.7zip", Apt: "p7zip-full"},
	{ID: "notepadplusplus", Name: "Notepad++", Category: "Utilities", Winget: "Notepad++.Notepad++"},
	{ID: "powertoys", Name: "Microsoft PowerToys", Category: "Utilities", Winget: "Microsoft.PowerToys"},
	{ID: "sharex", Name: "ShareX", Category: "Utilities", Winget: "ShareX.ShareX"},
	{ID: "everything", Name: "Everything (instant file search)", Category: "Utilities", Winget: "voidtools.Everything"},

	{ID: "zoom", Name: "Zoom", Category: "Communication", Winget: "Zoom.Zoom"},
	{ID: "teams", Name: "Microsoft Teams", Category: "Communication", Winget: "Microsoft.Teams"},
	{ID: "slack", Name: "Slack", Category: "Communication", Winget: "SlackTechnologies.Slack"},
	{ID: "thunderbird", Name: "Mozilla Thunderbird", Category: "Communication", Winget: "Mozilla.Thunderbird", Apt: "thunderbird"},

	{ID: "vscode", Name: "Visual Studio Code", Category: "Development", Winget: "Microsoft.VisualStudioCode", Apt: "code"},
	{ID: "git", Name: "Git", Category: "Development", Winget: "Git.Git", Apt: "git"},
	{ID: "python", Name: "Python 3", Category: "Development", Winget: "Python.Python.3.13", Apt: "python3"},
	{ID: "powershell", Name: "PowerShell 7", Category: "Development", Winget: "Microsoft.PowerShell"},
	{ID: "winscp", Name: "WinSCP", Category: "Development", Winget: "WinSCP.WinSCP"},
	{ID: "putty", Name: "PuTTY", Category: "Development", Winget: "PuTTY.PuTTY", Apt: "putty"},
}

// Catalog returns the built-in program list.
func Catalog() []App { return builtin }

// Get returns the app with this picker id, or false.
func Get(id string) (App, bool) {
	for _, a := range builtin {
		if strings.EqualFold(a.ID, id) {
			return a, true
		}
	}
	return App{}, false
}

// WingetIDs maps picker ids to winget package ids, preserving the order given.
// An unknown id, or one with no Windows package, is named in the error rather
// than silently dropped — a program the operator asked for and did not get is
// worth failing the build over.
func WingetIDs(ids []string) ([]string, error) {
	var out []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		a, ok := Get(id)
		if !ok {
			return nil, &UnknownError{ID: id}
		}
		if a.Winget == "" {
			return nil, &UnavailableError{ID: id, Name: a.Name, OS: "Windows"}
		}
		out = append(out, a.Winget)
	}
	return out, nil
}

// UnknownError is an id that is not in the catalog.
type UnknownError struct{ ID string }

func (e *UnknownError) Error() string {
	return "unknown program " + e.ID + " — see `uplink apps` for the list"
}

// UnavailableError is a known program with no package for the target OS.
type UnavailableError struct{ ID, Name, OS string }

func (e *UnavailableError) Error() string {
	return e.Name + " (" + e.ID + ") has no " + e.OS + " package in the catalog"
}

// Categories lists the distinct categories in catalog order.
func Categories() []string {
	var out []string
	seen := map[string]bool{}
	for _, a := range builtin {
		if !seen[a.Category] {
			seen[a.Category] = true
			out = append(out, a.Category)
		}
	}
	return out
}
