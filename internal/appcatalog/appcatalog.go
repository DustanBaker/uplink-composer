// Package appcatalog is the list of programs Quick Install can add to a
// machine: a built-in set naming packages in the platform's own package
// manager — winget on Windows, apt on Debian/Ubuntu, so installers come from
// the vendor at first boot and nothing large rides on the media — plus any
// installers the operator has added themselves, which do ride on the media
// because no package manager has them. See custom.go.
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
	// Custom is set when this is an installer the operator supplied rather
	// than a package from winget. It carries the library blob and the silent
	// switches; see custom.go.
	Custom *Custom
}

// InstallsOnWindows reports whether this program can actually be put on a
// Windows machine — either winget knows it, or the operator supplied the
// installer. The pickers filter on this, because offering an option that
// cannot run is worse than not offering it at all.
func (a App) InstallsOnWindows() bool { return a.Winget != "" || a.Custom != nil }

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

// Catalog returns the built-in program list followed by the operator's own
// installers, so every picker shows both without knowing the difference.
func Catalog() []App {
	cs := CustomApps()
	out := make([]App, 0, len(builtin)+len(cs))
	out = append(out, builtin...)
	for i := range cs {
		c := cs[i]
		cat := c.Category
		if cat == "" {
			cat = CustomCategory
		}
		out = append(out, App{ID: c.ID, Name: c.Name, Category: cat, Custom: &c})
	}
	return out
}

// Get returns the app with this picker id, or false.
func Get(id string) (App, bool) {
	for _, a := range Catalog() {
		if strings.EqualFold(a.ID, id) {
			return a, true
		}
	}
	return App{}, false
}

// Resolve splits picker ids into the two things that have to happen at first
// boot: winget package ids to install from the network, and operator-supplied
// installers that ride on the media and are run directly. Order is preserved
// within each.
//
// An unknown id, or one with no way to install on Windows, is named in the
// error rather than silently dropped — a program the operator asked for and
// did not get is worth failing the build over, because nobody audits a
// freshly imaged machine for a missing agent.
func Resolve(ids []string) (winget []string, custom []Custom, err error) {
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		a, ok := Get(id)
		if !ok {
			return nil, nil, &UnknownError{ID: id}
		}
		switch {
		case a.Custom != nil:
			custom = append(custom, *a.Custom)
		case a.Winget != "":
			winget = append(winget, a.Winget)
		default:
			return nil, nil, &UnavailableError{ID: id, Name: a.Name, OS: "Windows"}
		}
	}
	return winget, custom, nil
}

// WingetIDs maps picker ids to winget package ids. Kept for callers that only
// care about the network-installed half; Resolve is what a build wants.
func WingetIDs(ids []string) ([]string, error) {
	w, _, err := Resolve(ids)
	return w, err
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
	for _, a := range Catalog() {
		if !seen[a.Category] {
			seen[a.Category] = true
			out = append(out, a.Category)
		}
	}
	return out
}
