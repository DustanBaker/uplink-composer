# Plan: installing programs with Linux

Decisions marked **Decide** are Dusty's.

**Step 1 done (2026-09-14):** DSKY's autoinstall media, built from the real
ISOs, installed Ubuntu in a KVM virtual machine (`ubuntu-autoinstall` workflow,
run 34855847949):

| Case | Result |
|---|---|
| Server 24.04, zero-touch | Installed with no input in 4 min; answers, apt package, account, late-command and snap all present; booted |
| Server 26.04, zero-touch | Same, 5 min |
| Desktop 26.04, zero-touch | Same, 14 min; booted to the desktop |
| Desktop 26.04, prompt kept, no account | The installer read DSKY's answers and stopped on "Ready to install — Review your choices", listing them, with an Install button: the one confirmation before erasing |

Two things learned for step 2: snaps listed in the answers are installed on
first boot, not during install; and releases.ubuntu.com can be too slow to rely
on, which is why DSKY now downloads Ubuntu from the fastest mirror (v0.7.15).
Still to see: what the Desktop installer asks after Install when the answers
carry no account.

**Decided (2026-09-14):** keep Ubuntu's "Continue with autoinstall?" prompt on
Desktop (skip it on Server); leave unofficial clients out; keep passwords out
of DSKY for now, so the account is not in DSKY's answers. **Step 1** (booting
real Ubuntu ISOs in a VM) is built as `test/autoinstall/vm.sh` and the
`ubuntu-autoinstall` workflow; nothing after it is.

## Where it is today

- **Picking programs is Windows only.** Choosing a program for any Linux
  entry fails with "installing programs alongside … is not supported yet — it
  needs an autoinstall recipe".
- **A Linux entry writes the distro's own ISO to the stick, unchanged.** The
  installer runs as the distro made it, so DSKY has nowhere to add anything.
- **One exception, not yet proven:** a workspace recipe can make an *Ubuntu
  Server* stick install itself with nobody at the keyboard. DSKY appends a
  small CIDATA partition holding the cloud-init answers and rewrites GRUB, at
  the same length, to boot with `autoinstall`. The answer file already
  installs a package (`openssh-server`). **That has only been tested against a
  synthetic ISO. It has never booted a real Ubuntu ISO**, which makes it step 1
  below.
- **What there is to start from:** the program list has an `Apt` field on 12
  of its 92 entries, unused by anything.

## How Linux installers can be given a program list

The 27 entries fall into four groups, and each takes answers differently.

| Group | Entries | How answers get in | Programs possible |
|---|---|---|---|
| Ubuntu installer (subiquity) | Ubuntu Server 24.04 and 26.04, Ubuntu Desktop 26.04 | cloud-init `autoinstall`: DSKY already builds this | Yes, during install |
| Anaconda with kickstart | Fedora Server 44, AlmaLinux 10, Rocky 10, RHEL 10 | Anaconda looks for a volume labelled **OEMDRV** holding `ks.cfg`, with no boot option needed. DSKY could append that partition the way it appends CIDATA | Yes, during install |
| Live desktops with their own installer | Fedora Workstation, Linux Mint, Pop!_OS, CachyOS, Garuda, Nobara, PikaOS, openSUSE, Bazzite | None that works unattended | Only afterwards, by a script run from the stick |
| No fit | Arch (installer shell), NixOS (declarative), Omarchy, SteamOS recovery, Raspberry Pi OS, Proxmox VE, TrueNAS SCALE, Debian netinst | Various, or not a desktop at all | Not planned (Debian could use preseed later) |

## 1. Ubuntu first

Ubuntu is the recommendation because the hard part, getting answers into the
installer, is already written.

### Prove the path on a real ISO

Before any picker work, build a stick image from the real Ubuntu Server 26.04
ISO with DSKY's autoinstall, and boot it in a virtual machine:

- **Where:** a GitHub workflow, since GitHub's Linux runners provide KVM, or on
  kessel after `pacman -S qemu-full edk2-ovmf`.
- **Pass means:** the VM installs with no input, reboots, and a check over
  serial console or SSH finds `/etc/dsky-provisioned` and the packages.
- **Then Ubuntu Desktop 26.04,** whose installer is newer and whose GRUB menu
  is different. Whether the same same-length GRUB rewrite works there is
  unknown until it boots.

Nothing touches a real disk. If either ISO fails, fixing that comes before
programs.

### Where each program comes from

Ubuntu has three sources. The rule, in order:

1. **Ubuntu's own archive (apt)** when it has the program. It comes from
   Ubuntu and is updated with the system.
2. **Snap Store** when the publisher is the vendor (Snapcraft marks it
   *verified*) or Snapcraft's own team (*starred*). Ubuntu installs snaps
   natively, and the installer has a `snaps:` section for them.
3. **Flathub** for the rest, preferring publisher-verified apps. Ubuntu does
   not ship Flatpak, so the first one adds `flatpak` and the Flathub remote.
4. **The vendor's own apt repository** only where that is the vendor's
   official channel and nothing above is: Google Chrome.

Checked on 2026-09-14 against packages.ubuntu.com (noble = 24.04,
resolute = 26.04), the Snapcraft store API and the Flathub API:

| Program | Ubuntu source | Notes |
|---|---|---|
| Firefox, Thunderbird, LibreOffice | snap (Canonical/Mozilla, verified) | On Ubuntu the apt names install the snap anyway |
| VLC, GIMP, Inkscape, Blender, Audacity, OBS Studio, HandBrake, calibre, KeePassXC, BleachBit, qBittorrent, Remmina (for Remote Desktop) | apt | All in both 24.04 and 26.04 |
| Wireshark, Nmap, WireGuard, OpenVPN, PuTTY, 7-Zip (`7zip`), Git, Python 3, Node.js, Docker (`docker.io`), Java 21 (`openjdk-21-jre`), Steam (`steam-installer`) | apt | Both releases |
| Brave, Opera, Vivaldi, Slack, Telegram, ONLYOFFICE, Bitwarden, Spotify, Plex, Postman, Tailscale | snap, verified publisher | |
| Discord, Signal | snap, Snapcrafters (starred) | Also on Flathub |
| VS Code, PowerShell | snap, verified, **classic** confinement | Installer needs `classic: true` |
| Obsidian | Flathub (verified) | Its snap is classic and unverified |
| LibreWolf, 1Password, Epic Games and GOG (both through Heroic Games Launcher) | Flathub, verified | |
| Zoom, Dropbox, AnyDesk, GitHub Desktop, Zotero | Flathub, unverified | Zoom and Zotero snaps are unofficial too |
| Google Chrome | Google's apt repository | The Flathub Chrome is an unverified wrapper |
| Microsoft Teams | none official | Only "Teams for Linux", an unofficial client: leave out, or label it |
| Notion | none official | Unofficial snap only: leave out |

**Not on Linux, so hidden when the OS is Linux:** Microsoft 365 Apps, Webex,
TeamViewer (no Flathub, deb only), OneDrive, Google Drive, Box, Malwarebytes,
PowerToys, Notepad++, ShareX, Everything, Flow Launcher, Sysinternals, WizTree,
WinDirStat, Rufus, CPU-Z, HWMonitor, HWiNFO, CrystalDiskInfo, WinMerge,
mRemoteNG, WinSCP, Paint.NET, IrfanView, K-Lite, Greenshot, VC++ and .NET
runtimes, Oracle Java, Windows Terminal, JetBrains Toolbox, EA app, Ubisoft
Connect, Nmap's Windows build. The picker shows only what the chosen OS can
install, so nothing Windows-only is offered for Ubuntu.

In code, each `App` gains `Snap` (with a classic flag), `Flatpak`, and uses its
existing `Apt`, and `InstallsOn(os)` replaces `InstallsOnWindows`.

### When they install, and the log

- **apt packages and snaps:** in the installer's own `packages:` and `snaps:`
  sections, so they are there on first boot. Both need the network during
  install. The server installer already expects it, and the desktop installer
  needs to be confirmed to.
- **Flatpaks and Chrome:** on first boot, from the installed system's
  cloud-init, retrying until online, because they are larger and more likely
  to fail on a slow link.
- **Log:** everything is logged to `/var/log/dsky-apps.log`, and one failure
  never stops the rest, the same as `apps.ps1` on Windows.

### What the Install dialog gains for Ubuntu

The same program picker, filtered to Linux. Autoinstall also has to be told
what Windows' local-account option covers today:

- **Computer name, user name and password.** Autoinstall needs the password as
  a SHA-512 crypt hash. DSKY computes that: a small pure-Go implementation, or
  a small dependency.
- **Disk:** autoinstall erases the first disk without asking, the same as
  DSKY's Windows sticks. The dialog says so, like the Windows entries' firmware
  notes.

**Decide:**

1. **Hands-off or one prompt:** the rewritten GRUB boots straight into the
   install, or DSKY leaves Ubuntu's "Continue with autoinstall?" question in,
   so a stick booted on the wrong machine stops once before erasing it.
   Recommended: keep the question on Desktop, skip it on Server.
2. **Unofficial clients** (Teams for Linux, Notion snap, Zoom snap): leave out,
   or include with an "unofficial" label.
3. **Account:** type a password in the dialog, or have Ubuntu ask for the
   account on first boot (the `identity` section left interactive). The second
   keeps passwords out of DSKY but means one stop at the keyboard.

## 2. Fedora Server, AlmaLinux, Rocky, RHEL: kickstart

The same idea with a different answer file, reusing the picker and the
program table:

- **The partition:** DSKY appends a small FAT partition labelled `OEMDRV` with
  `ks.cfg`, which Anaconda finds by itself.
- **The answer file:** its `%packages` installs dnf packages, and a `%post`
  section adds Flathub for the rest.
- **The table:** each program also needs a dnf name. Most of the apt names
  above match, and EPEL is needed for some on Alma and Rocky.
- **Prove first:** that the OEMDRV partition is picked up from the same stick
  the installer booted from, in a VM, before anything else here.

## 3. Everything with a live installer: a script on the stick

Mint, Pop!_OS, Fedora Workstation, CachyOS and the rest can't take answers.
What they can take is a script:

- **The partition:** DSKY appends a small `DSKY` partition holding
  `install-programs.sh` and the picks.
- **What the script does:** after installing the OS, plug the stick in and run
  it. It finds the package manager (apt, dnf, pacman, zypper), installs what
  the distro has, and uses Flathub for the rest. Bazzite and other atomic
  systems get Flatpak only.
- **Not hands-off:** it's one command, but it covers every distro.
- **Prove first:** appending a partition to a hybrid ISO has to be proven not
  to stop each ISO booting. Some are built with isohybrid MBR-only layouts,
  which the CIDATA code has never handled.

## 4. Keep the names honest

Extend the weekly catalog-health run, which already checks OS links and winget
IDs, to confirm every apt, snap and Flathub name in the table still exists.
Snap and Flathub have public APIs that answer 200 or 404, and apt names are
checked against packages.ubuntu.com for each supported release.

## Order and size

1. **Boot real Ubuntu Server and Desktop ISOs with autoinstall in a VM** (a
   day, most of it the workflow). Everything else waits on this.
2. **Ubuntu programs:** table in code, `InstallsOn(os)`, autoinstall sections,
   first-boot Flatpak script, dialog fields, VM check that programs arrived (2
   to 3 days).
3. **Weekly name check** (a couple of hours).
4. **Kickstart for Fedora Server and the RHEL family** (2 days, after proving
   OEMDRV in a VM).
5. **The after-install script for live-installer distros** (2 days, mostly
   proving each ISO still boots with a partition appended).

1 to 3 fit one release; 4 and 5 are separate.

## Not in this plan

- Arch, NixOS and Omarchy: each has its own install tool
  (`archinstall --config`, a Nix configuration), and a picker would fight it.
- Proxmox VE and TrueNAS SCALE: appliances, where desktop programs don't apply.
- Raspberry Pi OS: it has a first-boot customisation file, but it writes a
  finished system rather than running an installer, so it is a different
  feature.
- Your own installers (.msi/.exe) have no Linux equivalent here. A .deb or
  .rpm the operator adds could come later.
