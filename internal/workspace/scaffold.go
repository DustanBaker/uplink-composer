package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Scaffold creates a new workspace at dir for the named org. Refuses to
// overwrite existing files.
func Scaffold(dir, orgName string) error {
	if orgName == "" {
		return fmt.Errorf("org name is required")
	}
	orgID := slugify(orgName)
	files := map[string]string{
		"workspace.yaml":                  fmt.Sprintf(scaffoldWorkspaceYAML, orgName, orgID),
		".gitignore":                      scaffoldGitignore,
		"README.md":                       fmt.Sprintf(scaffoldReadme, orgName),
		"vars.local.yaml":                 scaffoldVarsLocal,
		"templates/autounattend.xml.tmpl": scaffoldUnattend,
		"recipes/example-win11.yaml":      scaffoldWinRecipe,
		"manifests/ubuntu-24.04-iso.yaml": scaffoldUbuntuManifest,
		"payload/.gitkeep":                "",
	}
	for rel := range files {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
			return fmt.Errorf("%s already exists — refusing to overwrite (init only creates fresh workspaces)", rel)
		}
	}
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = nonSlug.ReplaceAllString(strings.ToLower(s), "-")
	return strings.Trim(s, "-")
}

const scaffoldWorkspaceYAML = `version: 1
org:
  name: %q
  id: %q
defaults:
  locale: en-US
vars:
  # Workspace-wide template vars; recipes and vars.local.yaml override.
  admin_user: user
  admin_display_name: User
  admin_password: ""
  computer_name: "*"
`

const scaffoldGitignore = `# Machine-local secrets — never commit.
vars.local.yaml
# Download remnants.
*.part
`

const scaffoldVarsLocal = `# Machine-local values referenced as ${var:name} in recipes.
# This file is gitignored; put secrets here.
# admin_password: "hunter2"
`

const scaffoldReadme = `# %s — Composer workspace

Recipes, templates, and pinned-source manifests for building bootable
installation USB media with The Composer.

- ` + "`composer recipes list`" + ` — what can be built
- ` + "`composer sources pull <id>`" + ` — fetch a pinned source into the local library
- ` + "`composer sources import <id> <file>`" + ` — add a manually-downloaded file (e.g. a Windows ISO)
- ` + "`composer build <recipe>`" + ` — compose a bootable image
- ` + "`composer devices`" + ` / ` + "`composer flash <recipe> <device>`" + ` — write a USB stick

Multi-gigabyte binaries never live in this repo: manifests pin url + sha256
so any machine can re-fetch them.
`

// scaffoldUnattend generalizes the proven NUC autounattend.xml: wipes disk 0,
// forces the edition via a generic key, creates a local admin, auto-logs-on
// once, and runs the generated first-boot script via FirstLogonCommands
// (SetupComplete.cmd is skipped under firmware OEM keys — do not use it).
const scaffoldUnattend = `<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend">

  <settings pass="windowsPE">
    <component name="Microsoft-Windows-International-Core-WinPE" processorArchitecture="amd64"
               publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <SetupUILanguage><UILanguage>{{.Vars.locale}}</UILanguage></SetupUILanguage>
      <InputLocale>{{.Vars.locale}}</InputLocale>
      <SystemLocale>{{.Vars.locale}}</SystemLocale>
      <UILanguage>{{.Vars.locale}}</UILanguage>
      <UserLocale>{{.Vars.locale}}</UserLocale>
    </component>
    <component name="Microsoft-Windows-Setup" processorArchitecture="amd64"
               publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <DiskConfiguration>
        <!-- DANGER: wipes disk 0 without prompting. That is the point. -->
        <Disk wcm:action="add" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
          <DiskID>0</DiskID>
          <WillWipeDisk>true</WillWipeDisk>
          <CreatePartitions>
            <CreatePartition wcm:action="add">
              <Order>1</Order><Type>EFI</Type><Size>300</Size>
            </CreatePartition>
            <CreatePartition wcm:action="add">
              <Order>2</Order><Type>MSR</Type><Size>16</Size>
            </CreatePartition>
            <CreatePartition wcm:action="add">
              <Order>3</Order><Type>Primary</Type><Extend>true</Extend>
            </CreatePartition>
          </CreatePartitions>
          <ModifyPartitions>
            <ModifyPartition wcm:action="add">
              <Order>1</Order><PartitionID>1</PartitionID><Format>FAT32</Format><Label>System</Label>
            </ModifyPartition>
            <ModifyPartition wcm:action="add">
              <Order>2</Order><PartitionID>3</PartitionID><Format>NTFS</Format><Label>Windows</Label>
            </ModifyPartition>
          </ModifyPartitions>
        </Disk>
      </DiskConfiguration>
      <ImageInstall>
        <OSImage>
          <InstallTo>
            <DiskID>0</DiskID>
            <PartitionID>3</PartitionID>
          </InstallTo>
        </OSImage>
      </ImageInstall>
      <UserData>
        <AcceptEula>true</AcceptEula>
        <ProductKey>
          <!-- Generic edition-select key: forces the edition even when the
               firmware carries an OEM key for another one. Selects only;
               activation needs a real license. -->
          <Key>{{.Vars.edition_key}}</Key>
        </ProductKey>
      </UserData>
    </component>
  </settings>

  <settings pass="specialize">
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64"
               publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <ComputerName>{{.Vars.computer_name}}</ComputerName>
    </component>
  </settings>

  <settings pass="oobeSystem">
    <component name="Microsoft-Windows-International-Core" processorArchitecture="amd64"
               publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <InputLocale>{{.Vars.locale}}</InputLocale>
      <SystemLocale>{{.Vars.locale}}</SystemLocale>
      <UILanguage>{{.Vars.locale}}</UILanguage>
      <UserLocale>{{.Vars.locale}}</UserLocale>
    </component>
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64"
               publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <OOBE>
        <HideEULAPage>true</HideEULAPage>
        <HideOEMRegistrationScreen>true</HideOEMRegistrationScreen>
        <HideOnlineAccountScreens>true</HideOnlineAccountScreens>
        <HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE>
        <ProtectYourPC>3</ProtectYourPC>
      </OOBE>
      <UserAccounts>
        <LocalAccounts>
          <LocalAccount wcm:action="add" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
            <Name>{{xml .Vars.admin_user}}</Name>
            <Group>Administrators</Group>
            <DisplayName>{{xml .Vars.admin_display_name}}</DisplayName>
            <Password>
              <Value>{{xml .Vars.admin_password}}</Value>
              <PlainText>true</PlainText>
            </Password>
          </LocalAccount>
        </LocalAccounts>
      </UserAccounts>
      <!-- One auto-logon so FirstLogonCommands fire hands-off. -->
      <AutoLogon>
        <Enabled>true</Enabled>
        <LogonCount>1</LogonCount>
        <Username>{{xml .Vars.admin_user}}</Username>
        <Password>
          <Value>{{xml .Vars.admin_password}}</Value>
          <PlainText>true</PlainText>
        </Password>
      </AutoLogon>
      <FirstLogonCommands>
        <SynchronousCommand wcm:action="add" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
          <Order>1</Order>
          <CommandLine>cmd /c C:\Windows\Setup\Scripts\firstboot.cmd</CommandLine>
          <Description>Run first-boot drivers and payload</Description>
        </SynchronousCommand>
      </FirstLogonCommands>
    </component>
  </settings>

</unattend>
`

const scaffoldWinRecipe = `version: 1
id: example-win11
name: "Example Win11 Pro unattended stick"

os:
  # A manifest id (composer sources import example-win11-iso <path-to.iso>)
  # or switch to source_mode: tree with tree_path pointing at a captured
  # master stick directory.
  source: example-win11-iso
  type: windows
  source_mode: auto

target:
  scheme: mbr
  filesystem: fat32
  volume_label: ESD-USB
  size: auto
  min_stick: 8GiB
  boot: uefi-only

windows:
  ei_cfg: { edition: Professional, channel: Retail, vl: false }
  unattend:
    template: templates/autounattend.xml.tmpl
    vars:
      # Generic Win11 Pro edition-select key (public, selects edition only).
      edition_key: VK7JG-NPHTM-C97JM-9MPGT-3V66T
      locale: en-US
  # winpe_drivers: [intel-vmd-pack]   # boot-critical storage/NIC -> $WinpeDriver$/
  driver_packs: []
  #  - { ref: intel-lan-pack, install: pnputil-sweep }
  #  - { ref: intel-sst-cab, install: expand-then-sweep }
  #  - { ref: intel-wifi-exe, install: exe, args: ["-q", "-s"] }
  payload: []
  #  - { ref: my-rmm-agent-msi }
  firstboot:
    mode: generate
    steps:
      - drivers
  #    - wait: 10s
  #    - msi: { ref: my-rmm-agent-msi, args: ["/qn"] }

flash:
  verify: readback-sha256
`

const scaffoldUbuntuManifest = `id: ubuntu-24.04-iso
kind: os-image
format: iso
url: https://releases.ubuntu.com/24.04/ubuntu-24.04.3-live-server-amd64.iso
# No sha256 pin yet: `+ "`composer sources pull ubuntu-24.04-iso`" + ` downloads,
# prints the hash, and refuses to catalog until you either verify it against
# https://releases.ubuntu.com/24.04/SHA256SUMS and pin it here, or re-run
# with --pin-tofu.
sha256: ""
notes: "Ubuntu 24.04 LTS live server; raw-write hybrid ISO"
`
