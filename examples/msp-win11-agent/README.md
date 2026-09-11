# Example: unattended Win11 Pro stick with an RMM agent at first boot

The common MSP job as one recipe: wipe a small-form-factor PC, install
Windows 11 Pro with zero prompts, install the driver packs that model
needs, strip consumer bloat, and have the org's RMM / remote-access agent
checked in by the time the desktop appears.

| Job                                      | Recipe concept                                   |
|------------------------------------------|--------------------------------------------------|
| Official Windows media, fetched for you  | `manifests/win11-iso.yaml` with `provider: fido` |
| A captured golden stick instead of an ISO| `os.source_mode: tree` + `tree_path`             |
| Unattended install, local admin, no OOBE | `templates/autounattend.xml.tmpl` + vars         |
| Pin the edition                          | `windows.ei_cfg`                                 |
| Vendor INF packs kept in the repo        | `driver_packs` with `path:` + `pnputil-sweep`    |
| Big vendor cabs/exes, pinned by hash     | `manifests/*.yaml` + `sources pull`              |
| Bloat-free, still official media         | `windows.debloat`                                |
| Agent installs at FIRST BOOT             | `payload` ref + firstboot `msi:` step            |
| Write and verify the stick               | `composer flash sff-win11-agent <device>`        |

To use it: `composer init --org "Your Org" <dir>`, copy `recipe.yaml` into
`recipes/`, drop your INF packs under `Drivers/`, then import the
org-specific binaries once per machine:

```
composer sources import rmm-agent-msi  YourAgent.msi
composer sources import wifi-exe       WiFi-Driver64.exe
composer sources pull   win11-iso
composer build sff-win11-agent
composer flash sff-win11-agent <device>
```

Two production rules the linter enforces:

- The agent installs at FIRST BOOT, never baked into the image (cloned
  agents collide on identity in the RMM).
- First-boot work runs via FirstLogonCommands, never SetupComplete.cmd
  (skipped when Setup uses a firmware OEM key).
