# Plan: more programs to install, like Brave

**Built.** Dusty's answers: keep Games and qBittorrent (in no starter set),
starter sets yes as listed, typed winget ids yes. What was built differs from
the plan in three places:

- Labels were taken from each package's winget manifest rather than guessed:
  "installs for the first account only" is on the eleven packages whose every
  installer is `Scope: user` (Slack, Discord, Signal, Telegram, Notion,
  Spotify, Nmap, Windows Terminal, JetBrains Toolbox, Postman, Flow Launcher).
  Microsoft Teams is not labelled; its MSIX has no scope. Licence labels are on
  Microsoft 365 Apps, 1Password, TeamViewer, AnyDesk and Docker Desktop.
- Lookups use GitHub's git trees API, one folder level at a time without
  regard to case, rather than the contents API, which truncates at 1,000
  entries. It is in `internal/appcatalog/winget.go`, shared by the picker,
  `dsky apps winget <id>` and the weekly check.
- Starter sets also work on the command line as `set:business`, `set:home`
  and `set:it`.

The rest of this file is the plan as written.

## Where it is today

- **22 programs** in `internal/appcatalog/appcatalog.go`, each a winget package
  ID: Chrome, Firefox, Acrobat Reader, LibreOffice, ONLYOFFICE, VLC, GIMP,
  7-Zip, Notepad++, PowerToys, ShareX, Everything, Zoom, Teams, Slack,
  Thunderbird, VS Code, Git, Python, PowerShell, WinSCP, PuTTY.
- **How they install:** `apps.ps1` runs at first sign-in. It waits for winget
  and for the network, tries each package machine-wide, then falls back to
  per-user for packages that only ship a per-user installer. One failure never
  stops the rest, and everything is logged to `apps.log`.
- **How they're picked:** a checkbox list inside a collapsed "Install programs"
  section of the Install dialog.
- **Your own installers** (.msi/.exe added in the app) sit alongside them and go
  on the stick, needing no network.
- **Unproven:** no winget install has run at a real first boot yet.

## 1. A bigger list, every entry checked

Grow the list from 22 to about 90, chosen for the two audiences: an MSP setting up
business PCs, and someone reinstalling their own computer.

Every ID below was checked on 2026-09-14 against Microsoft's winget package
repository (`microsoft/winget-pkgs`), not guessed. All 22 current entries are
still valid.

| Category | Programs (winget ID) |
|---|---|
| Browsers | **Brave** (`Brave.Brave`), Opera (`Opera.Opera`), Vivaldi (`Vivaldi.Vivaldi`), LibreWolf (`LibreWolf.LibreWolf`) |
| Communication | Discord (`Discord.Discord`), Signal (`OpenWhisperSystems.Signal`), Telegram (`Telegram.TelegramDesktop`), Webex (`Cisco.Webex`) |
| Passwords & security | Bitwarden (`Bitwarden.Bitwarden`), 1Password (`AgileBits.1Password`), KeePassXC (`KeePassXCTeam.KeePassXC`), Malwarebytes (`Malwarebytes.Malwarebytes`) |
| Cloud storage | Google Drive (`Google.GoogleDrive`), Dropbox (`Dropbox.Dropbox`), OneDrive (`Microsoft.OneDrive`), Box (`Box.Box`) |
| Documents | Microsoft 365 Apps (`Microsoft.Office`), Foxit PDF Reader (`Foxit.FoxitReader`), PDF24 (`geeksoftwareGmbH.PDF24Creator`), Obsidian (`Obsidian.Obsidian`), Notion (`Notion.Notion`), Zotero (`DigitalScholar.Zotero`), calibre (`calibre.calibre`) |
| Media | Spotify (`Spotify.Spotify`), OBS Studio (`OBSProject.OBSStudio`), Audacity (`Audacity.Audacity`), HandBrake (`HandBrake.HandBrake`), Paint.NET (`dotPDN.PaintDotNet`), Inkscape (`Inkscape.Inkscape`), Blender (`BlenderFoundation.Blender`), K-Lite Codec Pack (`CodecGuide.K-LiteCodecPack.Standard`), IrfanView (`IrfanSkiljan.IrfanView`), Greenshot (`Greenshot.Greenshot`), Plex (`Plex.Plex`) |
| Remote access & VPN | TeamViewer (`TeamViewer.TeamViewer`), AnyDesk (`AnyDesk.AnyDesk`), Tailscale (`Tailscale.Tailscale`), WireGuard (`WireGuard.WireGuard`), OpenVPN (`OpenVPNTechnologies.OpenVPN`), Remote Desktop client (`Microsoft.RemoteDesktopClient`), mRemoteNG (`mRemoteNG.mRemoteNG`) |
| IT tools | Sysinternals Suite (`Microsoft.Sysinternals.Suite`), Wireshark (`WiresharkFoundation.Wireshark`), Nmap (`Insecure.Nmap`), WizTree (`AntibodySoftware.WizTree`), WinDirStat (`WinDirStat.WinDirStat`), Rufus (`Rufus.Rufus`), CPU-Z (`CPUID.CPU-Z`), HWMonitor (`CPUID.HWMonitor`), HWiNFO (`REALiX.HWiNFO`), CrystalDiskInfo (`CrystalDewWorld.CrystalDiskInfo`), BleachBit (`BleachBit.BleachBit`), WinMerge (`WinMerge.WinMerge`) |
| Runtimes | Visual C++ Redistributable (`Microsoft.VCRedist.2015+.x64`), .NET 8 Desktop Runtime (`Microsoft.DotNet.DesktopRuntime.8`), Java 21 (`EclipseAdoptium.Temurin.21.JRE`), Oracle Java (`Oracle.JavaRuntimeEnvironment`) |
| Development | Windows Terminal (`Microsoft.WindowsTerminal`), Node.js LTS (`OpenJS.NodeJS.LTS`), Docker Desktop (`Docker.DockerDesktop`), JetBrains Toolbox (`JetBrains.Toolbox`), Postman (`Postman.Postman`), GitHub Desktop (`GitHub.GitHubDesktop`) |
| Games | Steam (`Valve.Steam`), Epic Games (`EpicGames.EpicGamesLauncher`), GOG Galaxy (`GOG.Galaxy`), EA app (`ElectronicArts.EADesktop`), Ubisoft Connect (`Ubisoft.Connect`) |
| Utilities | qBittorrent (`qBittorrent.qBittorrent`), Flow Launcher (`Flow-Launcher.Flow-Launcher`) |

**Not in winget, so not in the list:** RustDesk, FortiClient VPN and the NVIDIA
app. Those go through **Your installers** (add the vendor's .msi/.exe once, and
it rides on every stick). The list can say so when someone searches for one.

**Left out on purpose:** Microsoft Edge (Windows already has it).

Things to label on entries, because they change what someone gets:

- **Installs for the first account that signs in** (per-user installers such as
  Discord, Spotify, Slack, Notion): other accounts on that PC won't have it.
  Found by trying machine scope first, as the script already does; the label
  comes from knowing which packages only offer per-user.
- **Needs a licence or sign-in to use** (Microsoft 365 Apps, 1Password,
  TeamViewer for business use). Installing is not licensing.
- **Large downloads** (Microsoft 365 Apps, Docker Desktop, Blender), because
  first boot waits for them.

**Decide:**

1. Keep **Games** and **qBittorrent**? Useful for home reinstalls, often unwanted
   on business PCs. They could stay but never appear in a starter set (below).
2. Any programs missing from the table.

## 2. A searchable picker instead of a checkbox list

Ninety checkboxes in a collapsed section don't work. Reuse the model picker from
v0.7.8 (search box, results as you type, picks as removable chips) for programs:

- Search by name, and show the category beside each result.
- Typing a name that isn't in winget, like "RustDesk", says so and points at
  **Add installer…**.
- Your own installers appear in the same search, marked "on the stick".

**Starter sets** (optional): one click adds a common group, which can then be
edited like any other picks.

- *Business PC:* Chrome, Acrobat Reader, 7-Zip, Zoom, Teams, VC++ Redistributable
- *Home PC:* Brave or Chrome, VLC, 7-Zip, Spotify, Discord
- *IT technician:* Sysinternals, Wireshark, WizTree, Notepad++, PowerShell 7, PuTTY

**Decide:** 3. Starter sets, yes or no, and what goes in each.

## 3. Keep the list honest

Package IDs change: Python's ID has the version in it, and vendors rename.
Checking by hand once is not enough.

- **Weekly check:** extend `.github/workflows/catalog-health.yml` (which already
  checks OS download links every Monday) to confirm every winget ID still exists
  in `microsoft/winget-pkgs`, and open an issue when one disappears. The check
  looks up each package's folder directly. A directory listing is truncated at
  1,000 entries, which is how the first attempt at this check wrongly reported
  AnyDesk and Tailscale missing.
- **Unit test:** IDs unique, every entry has a category, names unique.

## 4. Later, maybe: any winget package

Beyond the curated list, let someone type any winget package ID or search all of
winget (tens of thousands of packages).

- **Typed ID** is cheap: accept `Publisher.Package`, confirm it exists the same
  way the weekly check does, and add it. Recommended if anything.
- **Searching all of winget** is not cheap. The full index Microsoft publishes is
  a SQLite database inside an MSIX package. Reading it without a C compiler means
  a large pure-Go SQLite dependency or a third-party search service, and the
  portal's rule of making no network requests from the page stays either way.
  Not recommended for now.

**Decide:** 4. Typed winget IDs, yes or no.

## Order and size

1. **List and labels** (section 1) plus the unit test: about half a day, mostly
   writing and double-checking the table in code.
2. **Picker** (section 2): about a day, reusing the model picker, with the same
   headless browser checks.
3. **Weekly check** (section 3): a couple of hours.
4. **Typed IDs** (section 4), if wanted: half a day.

All four fit one release.

## The part that still needs a real machine

None of this proves winget installs work at first boot, because that has never
run on real hardware. The first Windows install on the OptiPlex 3070 should
include two or three programs, including one per-user one such as Spotify, and
`C:\Windows\Setup\Scripts\apps.log` read afterwards. More programs make that
test more important, not less.
