<h1 align="center"><img src="docs/logo/dsky-cyan.png" alt="DSKY" width="480"></h1>

One tool to build bootable installation USB media for a fleet: pull and store
OS images, keep per-hardware driver packs, compose unattended install media
per machine/org (drivers + unattend + first-boot agents), and write verified
USB sticks. Runs on Windows, macOS, and Linux from a single static binary.

Org-agnostic by design: everything specific to an organization — recipes,
unattend templates, pinned download manifests, driver packs — lives in that
org's own **workspace** (a small git repo). `dsky init` scaffolds one in
seconds.

## About the name

DSKY — pronounced "DISS-kee" — was the astronauts' only interface to the Apollo
Guidance Computer: a numeric keypad and a display, driven by Verb/Noun number
pairs. Verb 37 Noun 01 was not a menu item. It was most of the vocabulary there
was, and it flew to the Moon.

What it shares with this tool is the situation rather than the styling. The
DSKY was the one panel between a person and a machine that would otherwise do
nothing useful, in a setting where a wrong entry was expensive and the display
had to say plainly what it was about to do.

<!-- TODO: dedication goes here. -->

## What it produces

- **Windows 10/11 unattended installers** — FAT32, UEFI-boot, `autounattend.xml`
  + `ei.cfg` + `$OEM$` payload; drivers install at first boot via `pnputil`
  (never DISM-injected), agents install at first boot (never baked — cloned
  agent identities collide in RMMs). WIMs over FAT32's 4 GiB limit are split
  automatically (DISM on Windows hosts, wimlib elsewhere).
- **Linux distro installers** — hybrid ISOs raw-written and verified; Ubuntu
  server can be made **zero-touch**: a recipe's `linux.autoinstall` renders
  cloud-init user-data into an appended CIDATA partition and rewrites the
  ISO's GRUB menu in place (same byte length, no ISO rebuild) to boot with
  `autoinstall`, so the installer never asks a question.
- **Appliance images** — raw `.img` (xz/zstd/gz-compressed supported),
  stream-decompressed while writing.

UEFI-only by explicit constraint. Legacy BIOS boot is out of scope.

## How it works

**Compose to image, then flash.** DSKY builds a raw disk image file
(partition table + FAT32 + files) entirely in userspace — no admin rights, no
mounting — then the flash engine raw-writes it to the stick and verifies by
readback hash. One identical build path on all three OSes; images are
reproducible byte-for-byte (`SOURCE_DATE_EPOCH`), cacheable, and safe to
archive as masters.

Multi-gigabyte binaries never live in git. Workspace **manifests** pin
`url` + `sha256` (`dsky sources pull`); everything lands in a
machine-local content-addressed **library**. Official Windows ISOs need no
browser dance: a manifest with `provider: fido` resolves Microsoft's
rotating download links at pull time through the hash-pinned
[Fido](https://github.com/pbatard/Fido) helper, so
`dsky sources pull win11-iso` goes straight from nothing to the current
official Pro ISO.

**Bloat-free by recipe, not by modified media.** `windows.debloat` (presets
`standard`/`aggressive`, plus `remove_apps`/`keep_apps` overrides) generates
a first-boot pass that strips consumer apps (Xbox, Bing, Clipchamp, consumer
Teams, Solitaire, …), turns off Copilot, widgets, advertising ID, consumer
promotions and Start suggestions, and sets telemetry to the Pro floor — while
the installed media itself stays official, fully updatable, and
activation-safe.

**Driver assistance — find, not just stage.** `dsky drivers search dell
"OptiPlex 7010"` (or `lenovo`/`hp` by model) pulls the vendor's own
enterprise driver-pack catalog and lists the matching packs with versions,
dates, sizes, and hashes; `--add` writes a pinned, self-describing manifest
and downloads it. For hardware without a vendor feed — Intel NUCs, ASUS, a
lone unknown NIC — `dsky drivers search mscatalog "PCI\VEN_8086&DEV_15B8"`
queries the Microsoft Update Catalog by hardware ID and pulls the official
signed driver cab. A recipe names the machines it serves in a
`windows.hardware` block, and `compose` resolves and stages their packs
automatically (`dsky drivers resolve <recipe>` does it up front). The
manual tools remain: `drivers inspect` reads INFs (ANSI or UTF-16) out of a
directory/zip/cab and reports class, versions, and hardware IDs; `drivers
add` stages a pack you already have; `drivers scan` lists the local
machine's devices that still need drivers, with the IDs to search for.

Safety. The disk the running OS lives on is refused outright; that is not a
confirmation anyone can click through. Install media is written only to
removable USB. Copying a drive and the disk utility can also write to a fixed
disk, but only when a person picked that disk for that job, and it is
deliberately harder than writing to a stick. Before anything destructive, the
confirmation describes the target the way its owner would recognise it —
model, size, serial, partitions, labels, where it is mounted — and then asks
for its exact size to be typed. Stale GPT backup headers are wiped, and every
write is verified by readback (which also catches counterfeit flash). Reading
is never restricted: the system disk can be the source of a copy.

## Install (no admin rights needed)

Windows (PowerShell):

```
irm https://raw.githubusercontent.com/uplinkresearch/dsky/main/install.ps1 | iex
```

macOS / Linux:

```
curl -fsSL https://raw.githubusercontent.com/uplinkresearch/dsky/main/install.sh | sh
```

Both fetch the latest release binary for your machine, verify its SHA-256
against the release's checksum list, install it under your user profile
(`%LOCALAPPDATA%\Programs\dsky` or `~/.local/bin`), and put `dsky`
plus the `compose` alias on your user PATH. Nothing touches system
directories. (Private fork? Set `GITHUB_TOKEN` first.)

## Quick Install — pick an OS, no setup

Launch the app (Start-menu or app-launcher icon, or `dsky app`) and the home screen
has an **Install an OS** list, grouped into desktop, server, and single-board.
Pick one, choose a couple of options — for Windows: edition, **local account
vs. normal OOBE**, how much bloatware to strip, **drivers for this computer**,
**programs to install**, and an optional skip of the TPM/Secure-Boot/RAM
checks — plug in a stick, confirm its size, and it builds and flashes. No
workspace, no recipes. From the CLI:

```
dsky catalog
dsky install windows-11 --edition Pro --account local --debloat standard
```

Twenty-seven operating systems ship in the list today: Windows 11 and 10
(fetched from Microsoft on demand via Fido); Ubuntu 26.04 LTS desktop and
server plus 24.04 LTS server; Fedora 44 Workstation and Server; Debian 13;
Arch; Omarchy; CachyOS desktop and handheld; Linux Mint; Pop!_OS; Bazzite;
Nobara; Garuda; PikaOS; openSUSE Tumbleweed; NixOS 26.05; Red Hat Enterprise
Linux 10, AlmaLinux and Rocky Linux; Proxmox VE; TrueNAS SCALE; Raspberry Pi
OS; and Valve's Steam Deck recovery image.

Nothing is bundled. Each entry is a pinned pointer — URL plus SHA-256, or a
vendor checksum file for images whose URL always means "newest" — so the bytes
come from Microsoft, Canonical, Fedora or Valve directly and are verified on
arrival. They land in a machine-local library and are reused by later builds.

### When the download is refused

Microsoft rate-limits its ISO service to roughly one request per address per
day. Download the ISO yourself and hand it over once:

```
dsky install windows-11 --iso "C:\Users\you\Downloads\Win11.iso"
```

It is filed under that OS, so later builds skip the fetch. The wizard asks for
a path when it needs one, and the portal has a field for it.

### Drivers for the machine in front of you

`dsky detect` profiles this computer — make and model, CPU, GPUs, network
adapters, every PnP/PCI device — and says what it would fetch. Adding
`--drivers` to an install stages those drivers on the media, so the machine
comes up fully driven instead of hunting packs by hand:

```
dsky detect                 # what this computer is, and what it needs
dsky detect --resolve       # fetch those drivers now, cached for later
dsky install windows-11 --drivers
```

Dell, Lenovo, HP and Framework machines get their per-model driver pack;
everything else resolves per device through the Microsoft Update Catalog. Devices the catalogs
do not carry are reported and skipped rather than failing the build — Windows
Update covers most of them. GPU packages are large (easily a gigabyte each),
so driver media wants a 16 GB stick.

Building media for a machine you are *not* sitting at — the bench case, where
the target is a customer's fleet — names the model instead:

```
dsky install windows-11 --drivers-for "dell:OptiPlex 7010 Micro"
dsky drivers models dell        # every model Dell has a pack for
```

In the app, the Install dialog has a searchable list of every model Dell, HP,
Lenovo and Framework publish Windows drivers for. It is repeatable and combines
with `--drivers`: pnputil installs only what matches the hardware it finds, so
one stick can carry packs for several models.

Framework is different in one way. It publishes a driver *bundle* per model
rather than a pack of drivers, and the bundle is Framework's own installer. It
runs unattended at first boot, and only on the model it is for: on any other
computer it is skipped, and it is stopped if it runs past 90 minutes. That path
has not yet been run on a real Framework.

### Programs

`dsky apps` lists the ninety-odd programs that can be installed alongside
the OS, from browsers to IT tools and game launchers; `--apps` picks them. They
install at first boot through winget, so nothing large rides on the media and
every installer comes from the vendor. A starter set (`set:business`,
`set:home`, `set:it`) adds a common group, and any other winget package works
by its id:

```
dsky apps
dsky install windows-11 --drivers --apps set:business,brave,winget:Mozilla.Firefox.ESR
```

The machine needs to be online at first boot for these — which is what the
staged network drivers are for. Some packages only install for the first
account that signs in; the list says which. Programs winget doesn't have, such
as RustDesk, go in with `dsky apps add` and ride on the stick.

### Three ways in, one pipeline

The same Quick Install path is driven by the CLI above, by `dsky tui`
(a full-screen terminal wizard — no flags to remember, works over SSH), and
by the web portal (`dsky serve`, or the Start-menu icon). A fix in the
pipeline shows up in all three.

### Twenty sticks at once

```
dsky flash media.img --all              # every stick attached
dsky clone <device> --to all            # read a master, write it to blanks
```

Targets are written in parallel under a **single** elevation — one prompt for
the batch, because twenty prompts would only teach people to script around
them. Each stick is independent, with its own handle and readback verify, so a
dead one fails alone and the error names it.

The interlock scales rather than repeating: one stick still asks for its exact
size; several list every target and ask you to type how many.

`dsky clone <device>` on its own reads a working stick into the library as a
master image — the generalized "capture the golden stick" workflow the original
NUC kit was built on. (`capture` still works as the old name.)

### Keeping it current

```
dsky update --check
dsky update
```

Replaces this binary with the newest published release, verified against the
checksums published beside it. The portal shows a banner when one is available.

This is the "distro-hop / image a machine in two clicks" path. For repeatable,
branded, fleet imaging with agents and per-model drivers, use a **workspace**:

## Quick start — "compose this"

```
dsky init --org "Acme IT" acme-workspace
cd acme-workspace
dsky example-win11
```

That last line is the whole job: it pulls any pinned source that is missing
(the official Windows ISO straight from Microsoft, driver cabs, agents),
composes the media, finds the one USB stick you have plugged in, asks you
to type its size, elevates once (UAC / polkit) for the raw write, writes,
and verifies every byte by readback. In a workspace with a single recipe,
plain `dsky` does the same. `dsky --build-only <recipe>` stops after
building; `dsky <recipe> <device>` names the stick when several are
attached.

The long form is still there when you want the pieces:

```
dsky sources pull win11-iso        # or: sources import win11-iso <path>
dsky build example-win11
dsky devices
dsky flash example-win11 <device-id>
```

The only step that ever needs elevated rights is the raw write to the USB
device itself, and that is one prompt per stick — installing and everything
else runs as a normal user.

`dsky serve` opens the same workflow as a local web page (loopback-only,
token-protected): recipes, devices, one-click builds, an arm-then-flash
dialog with the typed-size interlock enforced server-side, and live progress
over SSE. **Browse…** opens the host's own folder chooser to pick a workspace,
since a browser cannot hand a server an absolute path.

`dsky doctor` checks the host: on Windows and macOS the ISO/WIM tooling
is built into the OS (Mount-DiskImage/hdiutil, DISM); Linux needs `7zz` and
`wimlib`.

## Status

Early but real: the FAT32 image builder passes a native acid test (Windows mounts
a composed image, `chkdsk` reports zero problems, 400+ files hash-identical
through the Windows FAT driver, byte-reproducible builds), the full Windows
pipeline — captured-master trees, ISO extraction, overlays, driver packs,
generated first-boot scripts — is integration-tested, and the first physical
stick flashed on Windows verified 413/413 files through the OS FAT driver.
An Ubuntu stick has been flashed and verified on real hardware. Driver
auto-resolve is verified live against all four catalogs. The web UI, the
terminal wizard, and self-update are working.

Known gaps, stated plainly:

- **No physical Windows install has been done end to end yet** with drivers
  and programs staged. That is the next acceptance milestone, and until it
  passes, the first-boot driver and winget paths are reviewed but unproven.
  Domain join has not yet joined a real domain.
- **Copying a drive has never touched a real disk.** The clone engine's
  logic is tested — fanning out to many targets, a stick pulled mid-write, a
  drive that stores something other than what it was sent, every mistaken
  pairing — but against an in-memory target. The platform write path it
  depends on is not exercised by any of that.
- **The race detector has never run** over the test suite.
- **Parallel multi-stick writing has not been run on more than one stick.**
  The engine is there and its guards are tested; the concurrency is not
  hardware-proven.
- **macOS and Linux flash paths** are written but not hardware-tested.
- **Windows Server** is not in the catalog. Fido cannot fetch it and its
  edition selection needs WIM image names read from a real ISO rather than
  guessed.
- **Binaries are unsigned**, so SmartScreen and Smart App Control will object,
  and self-update verifies integrity rather than authorship. Code signing is
  the fix for both.

## Development

```
go build ./cmd/dsky
go test ./... -short
```

Pure Go plus `github.com/diskfs/go-diskfs` for partition tables and FAT32
(with two workarounds found by the acid test: io/fs-style paths for reads,
and a post-pass fixing `..` cluster entries of top-level directories to 0
per the FAT spec). External tools (7-Zip, wimlib, DISM) are always invoked
as subprocesses, never linked.

MIT licensed.
