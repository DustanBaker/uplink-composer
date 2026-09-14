package oscatalog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// FindDownloadedISO looks in the user's Downloads folder for the Windows ISO
// this entry would otherwise fetch, as Microsoft names it (Win11_25H2_English_x64.iso),
// and returns the newest complete one.
//
// Microsoft rate-limits the on-demand fetch, so downloading the ISO in a
// browser is common, and then Set up and Install asked Microsoft again anyway
// unless the file was also typed into "Use an ISO you downloaded". Finding it
// lets the dialog fill that in. Windows only: its fetch is trust-on-first-use
// anyway, whereas distro ISOs are hash-pinned and an unverified file must not
// quietly stand in for one.
func FindDownloadedISO(e Entry) string {
	if e.Family != Windows || e.Fido == nil || e.Fido.Win == "" {
		return ""
	}
	name := regexp.MustCompile(`(?i)^Win` + regexp.QuoteMeta(e.Fido.Win) + `_.*\.iso$`)
	var best string
	var bestTime int64
	for _, dir := range downloadDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, d := range entries {
			if d.IsDir() || !name.MatchString(d.Name()) {
				continue
			}
			p := filepath.Join(dir, d.Name())
			// Firefox writes the .iso beside a .part until it finishes.
			if _, err := os.Stat(p + ".part"); err == nil {
				continue
			}
			if CheckISO(p) != nil {
				continue
			}
			if info, err := d.Info(); err == nil && info.ModTime().Unix() > bestTime {
				best, bestTime = p, info.ModTime().Unix()
			}
		}
	}
	return best
}

func downloadDirs() []string {
	var dirs []string
	if d := knownDownloads(); d != "" {
		dirs = append(dirs, d)
	}
	if home, err := os.UserHomeDir(); err == nil {
		d := filepath.Join(home, "Downloads")
		if len(dirs) == 0 || !strings.EqualFold(dirs[0], d) {
			dirs = append(dirs, d)
		}
	}
	return dirs
}
