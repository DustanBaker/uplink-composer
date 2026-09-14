package oscatalog

import "golang.org/x/sys/windows"

// knownDownloads is the Downloads folder as Windows knows it, which follows
// the user if they moved it to another drive.
func knownDownloads() string {
	p, err := windows.KnownFolderPath(windows.FOLDERID_Downloads, 0)
	if err != nil {
		return ""
	}
	return p
}
