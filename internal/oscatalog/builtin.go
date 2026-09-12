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
		Category:      Server,
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
		Category:      Server,
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
		Category:      Server,
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
		ID:            "fedora-44-server",
		Name:          "Fedora 44 Server",
		Family:        Linux,
		Category:      Server,
		Version:       "44-1.7",
		URL:           "https://dl.fedoraproject.org/pub/fedora/linux/releases/44/Server/x86_64/iso/Fedora-Server-dvd-x86_64-44-1.7.iso",
		SHA256:        "85837793bfa36db6bc709b4cecd2ec116951b87d9c53c3d95eb2fac8dcf7cf1f",
		Filename:      "Fedora-Server-dvd-x86_64-44-1.7.iso",
		Notes:         "Full server install DVD — no network needed during setup.",
		FirmwareNotes: "UEFI boot. Signed shim, so Secure Boot works out of the box on most firmware.",
	},
	{
		ID:       "nixos-26.05-minimal",
		Name:     "NixOS 26.05 (minimal)",
		Family:   Linux,
		Category: Server,
		Version:  "26.05",
		// The channel URL always serves that channel's newest build, so there
		// is no fixed hash; NixOS publishes a per-image .sha256 beside it.
		URL:           "https://channels.nixos.org/nixos-26.05/latest-nixos-minimal-x86_64-linux.iso",
		ChecksumsURL:  "https://channels.nixos.org/nixos-26.05/latest-nixos-minimal-x86_64-linux.iso.sha256",
		Filename:      "latest-nixos-minimal-x86_64-linux.iso",
		Notes:         "Console installer for the declarative, reproducible distro. Install from its shell.",
		FirmwareNotes: "UEFI boot. Unsigned — Secure Boot must be off.",
	},
	{
		ID:            "omarchy",
		Name:          "Omarchy",
		Family:        Linux,
		Version:       "4.0.3",
		URL:           "https://iso.omarchy.org/omarchy-4.0.3.iso",
		SHA256:        "03d60bc74306dca51f96e1a84b690871d8d606826b260edd0208962da8507d14",
		Filename:      "omarchy-4.0.3.iso",
		Notes:         "Opinionated Arch + Hyprland desktop, preconfigured and ready to use.",
		FirmwareNotes: "UEFI boot. Unsigned — Secure Boot must be off.",
	},
	{
		ID:            "cachyos-desktop",
		Name:          "CachyOS Desktop",
		Family:        Linux,
		Version:       "260809",
		URL:           "https://cdn77.cachyos.org/ISO/desktop/260809/cachyos-desktop-linux-260809.iso",
		SHA256:        "959f6577f45e25ee9fd8c220fd221b08e4ea79412c7315c0f922dd6d86d5e33c",
		Filename:      "cachyos-desktop-linux-260809.iso",
		Notes:         "Arch, tuned for speed: optimised packages and a custom kernel, with a graphical installer.",
		FirmwareNotes: "UEFI boot. Unsigned — Secure Boot must be off.",
	},
	{
		ID:       "cachyos-handheld",
		Name:     "CachyOS Handheld",
		Family:   Linux,
		Version:  "260628",
		URL:      "https://cdn77.cachyos.org/ISO/handheld/260628/cachyos-handheld-linux-260628.iso",
		SHA256:   "0567c61f21622e5b4b482a874c41076beeb9bd8c00f046fa40e5c8cfbe34d15f",
		Filename: "cachyos-handheld-linux-260628.iso",
		// The practical answer to "I want SteamOS on this": Valve's own image
		// only serves Steam Deck hardware, while this installs on any handheld
		// gaming PC and boots to the same kind of controller-first session.
		Notes:         "For handheld gaming PCs — Steam Deck, ROG Ally, Legion Go. Boots straight into a game session.",
		FirmwareNotes: "UEFI boot. Unsigned — Secure Boot must be off.",
	},
	{
		ID:       "steamos-deck-recovery",
		Name:     "SteamOS (Steam Deck recovery)",
		Family:   Linux,
		Category: Appliance,
		Image:    ImageRaw,
		Version:  "20260707.10 / 3.8.14",
		// Pinned to the dated build rather than the "latest" alias, because
		// the hash below is ours: Valve publishes no checksum (the .sha256
		// URL redirects to the image itself), so this was computed from a
		// download and pins the file against later change, not against the
		// first fetch. The alias would invalidate it at every new build.
		URL:      "https://steamdeck-images.steamos.cloud/recovery/steamdeck-oobe-repair-20260707.10-3.8.14.img.bz2",
		SHA256:   "4254ee02ec34ae8add9aceef1881a2ce675a9d0176171df92e0eaa1bf014c594",
		Filename: "steamdeck-oobe-repair-20260707.10-3.8.14.img.bz2",
		Notes: "Valve's recovery image, for Steam Deck hardware only — reinstalls or repairs SteamOS on a Deck. " +
			"Not a SteamOS installer for a PC; for handheld gaming PCs generally, see CachyOS Handheld.",
		FirmwareNotes: "Boot a powered-off Deck with Volume-Down held to pick this stick. " +
			"Unsigned — Secure Boot must be off.",
	},
	{
		ID:       "raspios-arm64",
		Name:     "Raspberry Pi OS (64-bit)",
		Family:   Linux,
		Category: Appliance,
		Version:  "2026-06-18 (trixie)",
		// A raw disk image, not an installer: written to the card or stick the
		// Pi boots from, and it runs as-is.
		Image:         ImageRaw,
		Arch:          "arm64",
		URL:           "https://downloads.raspberrypi.com/raspios_arm64/images/raspios_arm64-2026-06-19/2026-06-18-raspios-trixie-arm64.img.xz",
		SHA256:        "123287c05f27b0eebd8f65456f6369b8f6635fa50a3d440a4f9f6223bf58c8e2",
		Filename:      "2026-06-18-raspios-trixie-arm64.img.xz",
		Notes:         "For Raspberry Pi hardware, not a PC. Writes a ready-to-run system — no installer to sit through.",
		FirmwareNotes: "Boots on Pi 3/4/5 and Zero 2. A Pi 4 or 5 boots this from USB; earlier boards want an SD card.",
	},
	{
		ID:            "popos-24.04",
		Name:          "Pop!_OS 24.04",
		Family:        Linux,
		Version:       "24.04 build 20",
		URL:           "https://iso.pop-os.org/24.04/amd64/intel/20/pop-os_24.04_amd64_intel_20.iso",
		SHA256:        "a0ef3842ab710db4f4407cf3499560b59dddbbcd59bee17beab7b0e99dc22b4c",
		Filename:      "pop-os_24.04_amd64_intel_20.iso",
		Notes:         "System76's desktop, with their COSMIC environment. Intel/AMD build.",
		FirmwareNotes: "UEFI boot. Secured-core PCs need the 3rd-party UEFI CA enabled in firmware for Secure Boot.",
	},
	{
		ID:     "bazzite",
		Name:   "Bazzite",
		Family: Linux,
		// "stable" always points at the newest build, so there is no fixed
		// hash; the checksum file beside it names this same filename.
		Version:       "stable (rolling)",
		URL:           "https://download.bazzite.gg/bazzite-stable-amd64.iso",
		ChecksumsURL:  "https://download.bazzite.gg/bazzite-stable-amd64.iso-CHECKSUM",
		Filename:      "bazzite-stable-amd64.iso",
		Notes:         "Fedora Atomic built for gaming — Steam, controllers and drivers ready on first boot. Good on handhelds and desktops alike.",
		FirmwareNotes: "UEFI boot. Signed shim, so Secure Boot generally works.",
	},
	{
		ID:      "nobara-44",
		Name:    "Nobara 44",
		Family:  Linux,
		Version: "44 (2026-09-02)",
		// Pinned to the dated build, which is immutable, rather than carrying a
		// ChecksumsURL — those are for the rolling entries whose target file
		// changes. The hash below was computed from a download and then checked
		// against the project's own Nobara-44-Official-2026-09-02.iso.sha256sum;
		// the two agree.
		URL:           "https://nobara-images.nobaraproject.org/Nobara-44-Official-2026-09-02.iso",
		SHA256:        "a2dda79fd7f805b683134d16ae4644c12f76d95abad3f2cc61b57d4fe40e6871",
		Filename:      "Nobara-44-Official-2026-09-02.iso",
		Notes:         "Fedora with the codecs, drivers and gaming tweaks already applied — NVIDIA and OBS work out of the box.",
		FirmwareNotes: "UEFI boot. Unsigned kernel — Secure Boot must be off.",
	},
	{
		ID:     "opensuse-tumbleweed",
		Name:   "openSUSE Tumbleweed",
		Family: Linux,
		// Rolling, and its checksum file names the dated snapshot rather than
		// the "Current" alias used to fetch it — resolved by the single-hash
		// rule rather than by filename.
		Version:       "rolling",
		URL:           "https://download.opensuse.org/tumbleweed/iso/openSUSE-Tumbleweed-DVD-x86_64-Current.iso",
		ChecksumsURL:  "https://download.opensuse.org/tumbleweed/iso/openSUSE-Tumbleweed-DVD-x86_64-Current.iso.sha256",
		Filename:      "openSUSE-Tumbleweed-DVD-x86_64-Current.iso",
		Notes:         "SUSE's rolling release, with YaST and snapshot-based rollback.",
		FirmwareNotes: "UEFI boot. Signed shim, so Secure Boot generally works.",
	},
	{
		ID:            "proxmox-ve-9",
		Name:          "Proxmox VE 9.2",
		Family:        Linux,
		Category:      Server,
		Version:       "9.2-1",
		URL:           "https://enterprise.proxmox.com/iso/proxmox-ve_9.2-1.iso",
		SHA256:        "4e88fe416df9b527624a175f24c9aa07c714d3332afb1ee3dbf3879573ef2c6c",
		Filename:      "proxmox-ve_9.2-1.iso",
		Notes:         "Bare-metal hypervisor: KVM virtual machines and LXC containers, managed from a web console.",
		FirmwareNotes: "UEFI boot. Installs Debian underneath and takes the whole disk.",
	},
	{
		ID:            "truenas-scale",
		Name:          "TrueNAS (SCALE)",
		Family:        Linux,
		Category:      Server,
		Version:       "25.10.7",
		URL:           "https://download.sys.truenas.net/TrueNAS-SCALE-Goldeye/25.10.7/TrueNAS-SCALE-25.10.7.iso",
		SHA256:        "54ce9441ce66966a392e28f63604ca3c2c083d0bec4db7bb5af2f74f7a007c8e",
		Filename:      "TrueNAS-SCALE-25.10.7.iso",
		Notes:         "Storage appliance OS — ZFS, shares and apps. Installs to its own boot device, separate from the pool disks.",
		FirmwareNotes: "UEFI boot. Wants a dedicated boot drive; your data disks stay untouched.",
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
	var all []string
	for sc.Scan() {
		line := sc.Text()
		m := sha256Line.FindString(line)
		if m == "" {
			continue
		}
		if strings.Contains(line, e.Filename) {
			return strings.ToLower(m), nil
		}
		all = append(all, strings.ToLower(m))
	}
	// A per-file checksum (`<image>.sha256`) holds exactly one hash, and names
	// the resolved image rather than the "latest-" alias used to fetch it —
	// NixOS does this. One hash in the whole file is unambiguous, so take it.
	if len(all) == 1 {
		return all[0], nil
	}
	return "", fmt.Errorf("no sha256 for %s in its checksum file — the URL or filename may have moved", e.Filename)
}
