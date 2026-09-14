package appcatalog

import (
	"fmt"
	"sort"
	"strings"
)

// UbuntuSource is where one program comes from on Ubuntu.
//
// The order of preference, and why (docs/plan-linux-apps.md):
//  1. Ubuntu's own archive (Apt), when it has the program.
//  2. The Snap Store (Snap), when the publisher is the vendor (verified) or
//     Snapcrafters (starred). The installer has a snaps section for them.
//  3. Flathub (Flatpak), publisher-verified apps only. Ubuntu doesn't ship
//     Flatpak, so the first one installs it, on first boot.
//  4. The vendor's own apt repository (Repo), only where that is the vendor's
//     official channel and nothing above is: Google Chrome.
//
// Unofficial clients are left out (Dusty's decision): Teams for Linux, the
// Notion snap, the Zoom and Zotero snaps, and Flathub apps whose publisher
// isn't verified (Zoom, Dropbox, AnyDesk, GitHub Desktop, Zotero, the Chrome
// wrapper). Every name below was checked on 2026-09-14 against
// packages.ubuntu.com for 24.04 and 26.04, the Snapcraft store API and the
// Flathub API.
type UbuntuSource struct {
	Apt     string `json:"apt,omitempty"`
	Snap    string `json:"snap,omitempty"`
	Classic bool   `json:"classic,omitempty"` // snap needs classic confinement
	Flatpak string `json:"flatpak,omitempty"`
	Repo    string `json:"repo,omitempty"` // a vendor repository DSKY knows how to add
	// Note is shown beside the name when what installs isn't obviously the
	// program asked for.
	Note string `json:"note,omitempty"`
}

// RepoGoogleChrome is Google's apt repository for Chrome.
const RepoGoogleChrome = "google-chrome"

var ubuntu = map[string]UbuntuSource{
	"chrome":      {Repo: RepoGoogleChrome},
	"firefox":     {Snap: "firefox"},
	"brave":       {Snap: "brave"},
	"opera":       {Snap: "opera"},
	"vivaldi":     {Snap: "vivaldi"},
	"librewolf":   {Flatpak: "io.gitlab.librewolf-community"},
	"slack":       {Snap: "slack"},
	"thunderbird": {Snap: "thunderbird"},
	"discord":     {Snap: "discord"},
	"signal":      {Snap: "signal-desktop"},
	"telegram":    {Snap: "telegram-desktop"},

	"libreoffice": {Apt: "libreoffice"},
	"onlyoffice":  {Snap: "onlyoffice-desktopeditors"},
	"obsidian":    {Flatpak: "md.obsidian.Obsidian"},
	"calibre":     {Apt: "calibre"},

	"bitwarden": {Snap: "bitwarden"},
	"1password": {Flatpak: "com.onepassword.OnePassword"},
	"keepassxc": {Apt: "keepassxc"},

	"vlc":       {Apt: "vlc"},
	"spotify":   {Snap: "spotify"},
	"gimp":      {Apt: "gimp"},
	"obs":       {Apt: "obs-studio"},
	"audacity":  {Apt: "audacity"},
	"handbrake": {Apt: "handbrake"},
	"inkscape":  {Apt: "inkscape"},
	"blender":   {Apt: "blender"},
	"plex":      {Snap: "plex-desktop"},

	"tailscale":     {Snap: "tailscale"},
	"wireguard":     {Apt: "wireguard-tools"},
	"openvpn":       {Apt: "openvpn"},
	"remotedesktop": {Apt: "remmina", Note: "installs Remmina"},

	"wireshark": {Apt: "wireshark"},
	"nmap":      {Apt: "nmap"},
	"bleachbit": {Apt: "bleachbit"},

	"7zip":        {Apt: "7zip"},
	"qbittorrent": {Apt: "qbittorrent"},

	"java21": {Apt: "openjdk-21-jre"},

	"vscode":     {Snap: "code", Classic: true},
	"git":        {Apt: "git"},
	"python":     {Apt: "python3"},
	"powershell": {Snap: "powershell", Classic: true},
	"nodejs":     {Apt: "nodejs"},
	"docker":     {Apt: "docker.io"},
	"postman":    {Snap: "postman"},
	"putty":      {Apt: "putty"},

	"steam":     {Apt: "steam-installer"},
	"epicgames": {Flatpak: "com.heroicgameslauncher.hgl", Note: "installs Heroic Games Launcher"},
	"gog":       {Flatpak: "com.heroicgameslauncher.hgl", Note: "installs Heroic Games Launcher"},
}

func init() {
	for i := range builtin {
		if src, ok := ubuntu[builtin[i].ID]; ok {
			src := src
			builtin[i].Ubuntu = &src
		}
	}
}

// Target is an operating system a program list is resolved for.
type Target string

const (
	TargetWindows Target = "windows"
	TargetUbuntu  Target = "ubuntu"
)

// InstallsOn reports whether this program can be put on the target.
func (a App) InstallsOn(t Target) bool {
	switch t {
	case TargetUbuntu:
		return a.Ubuntu != nil
	default:
		return a.InstallsOnWindows()
	}
}

// UbuntuPlan is a program list resolved for Ubuntu: what the installer's own
// sections take, and what the first-boot script installs.
type UbuntuPlan struct {
	Apt      []string
	Snaps    []UbuntuSnap
	Flatpaks []string
	Repos    []string
}

// UbuntuSnap is one snap for the installer's snaps section.
type UbuntuSnap struct {
	Name    string
	Classic bool
}

// FirstBoot reports whether anything has to wait for the installed system.
func (p UbuntuPlan) FirstBoot() bool { return len(p.Flatpaks) > 0 || len(p.Repos) > 0 }

// Empty reports whether nothing is to be installed.
func (p UbuntuPlan) Empty() bool {
	return len(p.Apt) == 0 && len(p.Snaps) == 0 && !p.FirstBoot()
}

// ResolveUbuntu turns picker ids into an Ubuntu install plan. A program with
// no Ubuntu source is named in the error rather than dropped, as for Windows:
// nobody audits a fresh machine for the program that isn't there.
func ResolveUbuntu(ids []string) (UbuntuPlan, error) {
	var p UbuntuPlan
	seen := map[string]bool{}
	add := func(list *[]string, v string) {
		if !seen[v] {
			seen[v] = true
			*list = append(*list, v)
		}
	}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if strings.HasPrefix(id, WingetPrefix) {
			return UbuntuPlan{}, fmt.Errorf("%s is a Windows package; it can't be installed on Ubuntu", strings.TrimPrefix(id, WingetPrefix))
		}
		if setID, isSet := strings.CutPrefix(id, "set:"); isSet {
			set, ok := SetByID(setID)
			if !ok {
				return UbuntuPlan{}, fmt.Errorf("no starter set %q (there are %s)", setID, setIDs())
			}
			// A starter set is a convenience: on Ubuntu it brings what it can.
			var avail []string
			for _, sid := range set.Apps {
				if a, ok := Get(sid); ok && a.Ubuntu != nil {
					avail = append(avail, sid)
				}
			}
			sub, err := ResolveUbuntu(avail)
			if err != nil {
				return UbuntuPlan{}, err
			}
			for _, v := range sub.Apt {
				add(&p.Apt, v)
			}
			for _, sn := range sub.Snaps {
				if !seen["snap:"+sn.Name] {
					seen["snap:"+sn.Name] = true
					p.Snaps = append(p.Snaps, sn)
				}
			}
			for _, v := range sub.Flatpaks {
				add(&p.Flatpaks, v)
			}
			for _, v := range sub.Repos {
				add(&p.Repos, v)
			}
			continue
		}
		a, ok := Get(id)
		if !ok {
			return UbuntuPlan{}, &UnknownError{ID: id}
		}
		if a.Ubuntu == nil {
			if a.Custom != nil {
				return UbuntuPlan{}, fmt.Errorf("%s is a Windows installer you added; it can't be installed on Ubuntu", a.Name)
			}
			return UbuntuPlan{}, &UnavailableError{ID: id, Name: a.Name, OS: "Ubuntu"}
		}
		src := a.Ubuntu
		switch {
		case src.Apt != "":
			add(&p.Apt, src.Apt)
		case src.Snap != "":
			if !seen["snap:"+src.Snap] {
				seen["snap:"+src.Snap] = true
				p.Snaps = append(p.Snaps, UbuntuSnap{Name: src.Snap, Classic: src.Classic})
			}
		case src.Flatpak != "":
			add(&p.Flatpaks, src.Flatpak)
		case src.Repo != "":
			add(&p.Repos, src.Repo)
		}
	}
	sort.Strings(p.Repos)
	return p, nil
}
