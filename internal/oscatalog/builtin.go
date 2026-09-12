package oscatalog

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/DustanBaker/uplink-composer/internal/buildinfo"
	"github.com/DustanBaker/uplink-composer/internal/manifest"
)

// builtin is the shipped OS list. Windows resolves through Fido (always
// current, no hash to rotate); the Linux entries pin url + sha256. Point
// releases move the Linux URLs, so these get updated over time (the
// hosted-index catalog is the longer-term fix).
var builtin = []Entry{
	{
		ID:       "windows-11",
		Name:     "Windows 11",
		Family:   Windows,
		Version:  "latest (24H2+)",
		Editions: []string{"Pro", "Home", "Pro N", "Education", "Enterprise"},
		Provider: "fido",
		Fido:     &manifest.FidoSpec{Win: "11", Release: "Latest", Edition: "Pro", Language: "English", Arch: "x64"},
		Notes:    "Official Microsoft media, fetched on demand. Edition, local-account vs OOBE, and debloat are options.",
		FirmwareNotes: "UEFI boot. Disk 0 is wiped without prompting. On unsupported hardware, enable 'skip requirement checks'.",
	},
	{
		ID:       "windows-10",
		Name:     "Windows 10",
		Family:   Windows,
		Version:  "22H2",
		Editions: []string{"Pro", "Home", "Pro N", "Education", "Enterprise"},
		Provider: "fido",
		Fido:     &manifest.FidoSpec{Win: "10", Release: "22H2", Edition: "Pro", Language: "English", Arch: "x64"},
		Notes:    "Official Microsoft media, fetched on demand.",
		FirmwareNotes: "UEFI boot. Disk 0 is wiped without prompting.",
	},
	{
		ID:       "ubuntu-24.04-server",
		Name:     "Ubuntu 24.04 LTS Server",
		Family:   Linux,
		Version:  "24.04.5",
		URL:      "https://releases.ubuntu.com/24.04/ubuntu-24.04.5-live-server-amd64.iso",
		SHA256:   "97f3d7ffb032c3eb3b23d2c8be9cc76e60c2c1f2c0146ba5ba9fe01cafae0fd8",
		Filename: "ubuntu-24.04.5-live-server-amd64.iso",
		Notes:    "Boots the Ubuntu Server installer. (Zero-touch autoinstall is available in a workspace recipe.)",
		FirmwareNotes: "UEFI boot. Secured-core PCs need the 3rd-party UEFI CA enabled in firmware for Secure Boot.",
	},
	{
		ID:       "linuxmint-22.1-cinnamon",
		Name:     "Linux Mint 22.1 (Cinnamon)",
		Family:   Linux,
		Version:  "22.1",
		URL:      "https://mirrors.edge.kernel.org/linuxmint/stable/22.1/linuxmint-22.1-cinnamon-64bit.iso",
		SHA256:   "ccf482436df954c0ad6d41123a49fde79352ca71f7a684a97d5e0a0c39d7f39f",
		Filename: "linuxmint-22.1-cinnamon-64bit.iso",
		Notes:    "Boots the Linux Mint live desktop; install from there.",
		FirmwareNotes: "UEFI boot.",
	},
}

var sha256Line = regexp.MustCompile(`\b[0-9a-fA-F]{64}\b`)

// resolveChecksum fetches a distro's checksum file and returns the sha256 for
// the entry's ISO filename (handles both "<hash>  file" and
// "SHA256 (file) = <hash>" layouts).
func resolveChecksum(ctx context.Context, e Entry) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.ChecksumsURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching checksums for %s: %w", e.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("checksums for %s: HTTP %d", e.Name, resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(line, e.Filename) {
			continue
		}
		if m := sha256Line.FindString(line); m != "" {
			return strings.ToLower(m), nil
		}
	}
	return "", fmt.Errorf("no sha256 for %s in its checksum file — the URL or filename may have moved", e.Filename)
}
