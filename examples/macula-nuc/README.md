# Example: the Macula NUC stick as a Composer recipe

This maps the original `nuc-deployment-usb` kit (Win11 Pro + ScreenConnect
at first boot, ASUS NUC driver packs) onto Composer concepts. It is the
acceptance target for the Windows pipeline: the recipe below must produce a
stick byte-equivalent in behavior to the hand-built master.

| nuc-deployment-usb                     | Composer                                        |
|----------------------------------------|-------------------------------------------------|
| `/mnt/data/nuc-usb-master` (rsync'd)   | `os.source_mode: tree` + `tree_path`            |
| `autounattend.xml`                     | `templates/autounattend.xml.tmpl` + vars        |
| `sources/ei.cfg`                       | `windows.ei_cfg`                                |
| `install-agent.cmd`                    | `windows.firstboot` (mode: generate)            |
| `Drivers/intel-lan-*` INF packs        | `driver_packs` with `path:` + `pnputil-sweep`   |
| `fetch-sst.sh` (SHA-pinned cab)        | `manifests/intel-sst-cab.yaml` + `sources pull` |
| Wi-Fi vendor exe, silent               | `driver_packs` with `install: exe`              |
| ScreenConnect MSI at first boot        | `payload` ref + firstboot `msi:` step           |
| `make-nuc-usb.sh write /dev/sdX`       | `composer flash nuc-win11 <device>`             |

`recipe.yaml` here is the full translation. To use it for real, run
`composer init` in (or alongside) the nuc-deployment-usb repo, copy this
recipe into `recipes/`, add the SST-cab manifest, and import the
ScreenConnect MSI once per machine:

```
composer sources import macula-screenconnect-msi ScreenConnect.ClientSetup.msi
composer sources pull intel-sst-cab
composer build nuc-win11
composer flash nuc-win11 <device>
```

Two behavioral notes carried over from the original kit's hard-won rules —
the linter enforces both:

- The agent installs at FIRST BOOT, never baked into the image (cloned
  agents collide on identity in ScreenConnect).
- First-boot work runs via FirstLogonCommands, never SetupComplete.cmd
  (skipped when Setup uses a firmware OEM key).
