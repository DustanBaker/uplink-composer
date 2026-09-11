# The Composer

One tool to build bootable installation USB media for a fleet: pull and store
OS images, keep per-hardware driver packs, compose unattended install media
per machine/org (drivers + unattend + first-boot agents), and write verified
USB sticks. Runs on Windows, macOS, and Linux from a single static binary.

Org-agnostic by design: everything specific to an organization — recipes,
unattend templates, pinned download manifests, driver packs — lives in that
org's own **workspace** (a small git repo). `composer init` scaffolds one in
seconds.

## What it produces

- **Windows 10/11 unattended installers** — FAT32, UEFI-boot, `autounattend.xml`
  + `ei.cfg` + `$OEM$` payload; drivers install at first boot via `pnputil`
  (never DISM-injected), agents install at first boot (never baked — cloned
  agent identities collide in RMMs). WIMs over FAT32's 4 GiB limit are split
  automatically (DISM on Windows hosts, wimlib elsewhere).
- **Linux distro installers** — hybrid ISOs raw-written and verified.
- **Appliance images** — raw `.img` (xz/zstd/gz-compressed supported),
  stream-decompressed while writing.

UEFI-only by explicit constraint. Legacy BIOS boot is out of scope.

## How it works

**Compose to image, then flash.** The composer builds a raw disk image file
(partition table + FAT32 + files) entirely in userspace — no admin rights, no
mounting — then the flash engine raw-writes it to the stick and verifies by
readback hash. One identical build path on all three OSes; images are
reproducible byte-for-byte (`SOURCE_DATE_EPOCH`), cacheable, and safe to
archive as masters.

Multi-gigabyte binaries never live in git. Workspace **manifests** pin
`url` + `sha256` (`composer sources pull`); everything lands in a
machine-local content-addressed **library**. Official Windows ISOs need no
browser dance: a manifest with `provider: fido` resolves Microsoft's
rotating download links at pull time through the hash-pinned
[Fido](https://github.com/pbatard/Fido) helper, so
`composer sources pull win11-iso` goes straight from nothing to the current
official Pro ISO.

**Bloat-free by recipe, not by modified media.** `windows.debloat` (presets
`standard`/`aggressive`, plus `remove_apps`/`keep_apps` overrides) generates
a first-boot pass that strips consumer apps (Xbox, Bing, Clipchamp, consumer
Teams, Solitaire, …), turns off Copilot, widgets, advertising ID, consumer
promotions and Start suggestions, and sets telemetry to the Pro floor — while
the installed media itself stays official, fully updatable, and
activation-safe.

**Driver assistance.** `composer drivers inspect <pack>` reads INFs (ANSI or
UTF-16) out of a directory, zip, or cab and reports device class, provider,
versions, resolved device names, and hardware IDs; `drivers add` stages the
pack correctly for its type (INF dir → workspace, zip/cab/exe → library +
pinned manifest) and prints the recipe snippet; `drivers scan` lists the
local machine's devices that still need drivers, with the hardware IDs to
hunt for.

Safety, inherited from the shell script this tool generalizes: only
removable USB devices are ever flashable, the disk hosting the OS is
hard-refused, flashing requires typing the target's exact size, stale GPT
backup headers are wiped, and every write is verified by readback (which
also catches counterfeit flash).

## Quick start

```
composer init --org "Acme IT" acme-workspace
cd acme-workspace
composer sources import example-win11-iso D:\Downloads\Win11_24H2.iso
composer build example-win11
composer devices
composer flash example-win11 <device-id>
```

`composer serve` opens the same workflow as a local web page (loopback-only,
token-protected): recipes, devices, one-click builds, an arm-then-flash
dialog with the typed-size interlock enforced server-side, and live progress
over SSE. `composer capture <device>` reads a working stick (through its
last partition) into the library as a master image — the generalized
"capture the golden stick" workflow.

`composer doctor` checks the host: on Windows and macOS the ISO/WIM tooling
is built into the OS (Mount-DiskImage/hdiutil, DISM); Linux needs `7zz` and
`wimlib`.

## Status

Early but real: the FAT32 composer passes a native acid test (Windows mounts
a composed image, `chkdsk` reports zero problems, 400+ files hash-identical
through the Windows FAT driver, byte-reproducible builds), the full Windows
pipeline — captured-master trees, ISO extraction, overlays, driver packs,
generated first-boot scripts — is integration-tested, and the first physical
stick flashed on Windows verified 413/413 files through the OS FAT driver.
The web UI is live. macOS/Linux flash paths are written but not yet
hardware-tested; the Windows-ISO end-to-end and a physical unattended
install are the next acceptance milestones.

## Development

```
go build ./cmd/composer
go test ./... -short
```

Pure Go plus `github.com/diskfs/go-diskfs` for partition tables and FAT32
(with two workarounds found by the acid test: io/fs-style paths for reads,
and a post-pass fixing `..` cluster entries of top-level directories to 0
per the FAT spec). External tools (7-Zip, wimlib, DISM) are always invoked
as subprocesses, never linked.

MIT licensed.
