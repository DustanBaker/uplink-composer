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

// builtin is the shipped OS list.
//
// Windows resolves through Fido (always current, no hash to rotate). Linux
// entries pin url + sha256 where the artifact is immutable — a published
// point release never changes content, so a hash is the strongest pin
// available. Rolling images whose URL deliberately points at "whatever is
// newest" (Arch) cannot be hash-pinned without breaking monthly, so those
// carry a ChecksumsURL and are verified against the vendor's own checksum
// file at pull time instead.
//
// Point releases move these URLs, so entries need updating over time; the
// hosted-index catalog is the longer-term fix.
var builtin = []Entry{
	{
		ID:            "windows-11",
		Name:          "Windows 11",
		Family:        Windows,
		Version:       "latest (24H2+)",
		Editions:      []string{"Pro", "Home", "Pro N", "Education", "Enterprise"},
		Provider:      "fido",
		Fido:          &manifest.FidoSpec{Win: "11", Release: "Latest", Edition: "Pro", Language: "English", Arch: "x64"},
		Notes:         "Official Microsoft media, fetched on demand. Edition, local-account vs OOBE, and debloat are options.",
		FirmwareNotes: "UEFI boot. Disk 0 is wiped without prompting. On unsupported hardware, enable 'skip requirement checks'.",
	},
	{
		ID:            "windows-10",
		Name:          "Windows 10",
		Family:        Windows,
		Version:       "22H2",
		Editions:      []string{"Pro", "Home", "Pro N", "Education", "Enterprise"},
		Provider:      "fido",
		Fido:          &manifest.FidoSpec{Win: "10", Release: "22H2", Edition: "Pro", Language: "English", Arch: "x64"},
		Notes:         "Official Microsoft media, fetched on demand.",
		FirmwareNotes: "UEFI boot. Disk 0 is wiped without prompting.",
	},
	{
		ID:            "ubuntu-24.04-server",
		Name:          "Ubuntu 24.04 LTS Server",
		Family:        Linux,
		Version:       "24.04.5",
		URL:           "https://releases.ubuntu.com/24.04/ubuntu-24.04.5-live-server-amd64.iso",
		SHA256:        "97f3d7ffb032c3eb3b23d2c8be9cc76e60c2c1f2c0146ba5ba9fe01cafae0fd8",
		Filename:      "ubuntu-24.04.5-live-server-amd64.iso",
		Notes:         "Boots the Ubuntu Server installer. (Zero-touch autoinstall is available in a workspace recipe.)",
		FirmwareNotes: "UEFI boot. Secured-core PCs need the 3rd-party UEFI CA enabled in firmware for Secure Boot.",
	},
	{
		ID:            "ubuntu-26.04-desktop",
		Name:          "Ubuntu 26.04 LTS Desktop",
		Family:        Linux,
		Version:       "26.04.1",
		URL:           "https://releases.ubuntu.com/26.04/ubuntu-26.04.1-desktop-amd64.iso",
		SHA256:        "601e30fbf5d97759367c632e2c33630665039b7e2158fd068403da3ccf1bda1f",
		Filename:      "ubuntu-26.04.1-desktop-amd64.iso",
		Notes:         "Boots the live desktop; install from there. The current LTS.",
		FirmwareNotes: "UEFI boot. Secured-core PCs need the 3rd-party UEFI CA enabled in firmware for Secure Boot.",
	},
	{
		ID:            "ubuntu-26.04-server",
		Name:          "Ubuntu 26.04 LTS Server",
		Family:        Linux,
		Version:       "26.04.1",
		URL:           "https://releases.ubuntu.com/26.04/ubuntu-26.04.1-live-server-amd64.iso",
		SHA256:        "cc8a95cde20f6ced61a322420de00f10cc3c90ced545daa46cb9c1a117f1d927",
		Filename:      "ubuntu-26.04.1-live-server-amd64.iso",
		Notes:         "Boots the Ubuntu Server installer. The current LTS.",
		FirmwareNotes: "UEFI boot. Secured-core PCs need the 3rd-party UEFI CA enabled in firmware for Secure Boot.",
	},
	{
		ID:            "fedora-44-workstation",
		Name:          "Fedora 44 Workstation",
		Family:        Linux,
		Version:       "44-1.7",
		URL:           "https://dl.fedoraproject.org/pub/fedora/linux/releases/44/Workstation/x86_64/iso/Fedora-Workstation-Live-44-1.7.x86_64.iso",
		SHA256:        "1620295f6a00c27c3208f0c00b8ece4eab1ec69b9002152d97488bf26a426ddf",
		Filename:      "Fedora-Workstation-Live-44-1.7.x86_64.iso",
		Notes:         "Boots the Fedora live desktop; install from there.",
		FirmwareNotes: "UEFI boot. Signed shim, so Secure Boot works out of the box on most firmware.",
	},
	{
		ID:            "debian-13-netinst",
		Name:          "Debian 13 (netinst)",
		Family:        Linux,
		Version:       "13.6.0",
		URL:           "https://cdimage.debian.org/debian-cd/current/amd64/iso-cd/debian-13.6.0-amd64-netinst.iso",
		SHA256:        "65273beed27b2df543b68b65630ba525cfbad8df2b12035732b2dff87d6664e7",
		Filename:      "debian-13.6.0-amd64-netinst.iso",
		Notes:         "Small network installer — the target machine needs wired internet during setup.",
		FirmwareNotes: "UEFI boot. Signed shim, so Secure Boot works out of the box on most firmware.",
	},
	{
		ID:     "archlinux",
		Name:   "Arch Linux",
		Family: Linux,
		// Rolling: this URL always points at the newest monthly image, so
		// there is no stable hash to pin. Verified against Arch's own
		// checksum file at pull time instead.
		Version:       "monthly rolling",
		URL:           "https://geo.mirror.pkgbuild.com/iso/latest/archlinux-x86_64.iso",
		ChecksumsURL:  "https://geo.mirror.pkgbuild.com/iso/latest/sha256sums.txt",
		Filename:      "archlinux-x86_64.iso",
		Notes:         "Boots the Arch installer environment; run archinstall or install by hand.",
		FirmwareNotes: "UEFI boot. Unsigned — Secure Boot must be off.",
	},
	{
		ID:            "linuxmint-22.1-cinnamon",
		Name:          "Linux Mint 22.1 (Cinnamon)",
		Family:        Linux,
		Version:       "22.1",
		URL:           "https://mirrors.edge.kernel.org/linuxmint/stable/22.1/linuxmint-22.1-cinnamon-64bit.iso",
		SHA256:        "ccf482436df954c0ad6d41123a49fde79352ca71f7a684a97d5e0a0c39d7f39f",
		Filename:      "linuxmint-22.1-cinnamon-64bit.iso",
		Notes:         "Boots the Linux Mint live desktop; install from there.",
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
