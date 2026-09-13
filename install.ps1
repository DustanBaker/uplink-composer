# DSKY — no-admin installer for Windows.
#
#   irm https://raw.githubusercontent.com/uplinkresearch/dsky/main/install.ps1 | iex
#
# Downloads the latest release into %LOCALAPPDATA%\Programs\dsky — the same
# place the setup .exe uses, and the place `dsky uninstall` looks — verifies
# every file against the release's SHA256SUMS.txt, and adds that folder to
# the USER PATH. No administrator rights needed. Installs `dsky`, the
# `compose` alias, and the click-to-launch app. Set GITHUB_TOKEN to install
# from a private fork.
$ErrorActionPreference = 'Stop'
$repo = 'uplinkresearch/dsky'

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$headers = @{ 'User-Agent' = 'dsky-installer' }
if ($env:GITHUB_TOKEN) { $headers['Authorization'] = "Bearer $env:GITHUB_TOKEN" }
$dl = @{ 'User-Agent' = 'dsky-installer'; 'Accept' = 'application/octet-stream' }
if ($env:GITHUB_TOKEN) { $dl['Authorization'] = "Bearer $env:GITHUB_TOKEN" }

$rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -Headers $headers
$tag = $rel.tag_name

# Exact names, the way selfupdate.AssetName spells them. A wildcard such as
# "dsky-*-windows-amd64.exe" also matches the dsky-app build, and which of
# the two came first was up to the order GitHub listed them in.
function Find-Asset($name) { $rel.assets | Where-Object { $_.name -eq $name } | Select-Object -First 1 }
$cli  = Find-Asset "dsky-$tag-windows-$arch.exe"
$app  = Find-Asset "dsky-app-$tag-windows-$arch.exe"
$icon = Find-Asset 'dsky.ico'
$sums = Find-Asset 'SHA256SUMS.txt'
if (-not $cli)  { throw "release $tag has no windows-$arch build" }
if (-not $sums) { throw "release $tag publishes no SHA256SUMS.txt; refusing to install unverified" }
$sumText = Invoke-RestMethod -Uri $sums.url -Headers $dl

$dir = Join-Path $env:LOCALAPPDATA 'Programs\dsky'
New-Item -ItemType Directory -Force $dir | Out-Null
$ProgressPreference = 'SilentlyContinue'

# Save-Verified downloads beside the destination and only moves it into place
# once the hash matches, so a failed or tampered download never replaces a
# working install. A file the checksum list does not name is refused rather
# than trusted: self-update holds the same line.
function Save-Verified($asset, $dest) {
    Write-Host "Downloading $($asset.name) ($([math]::Round($asset.size / 1MB, 1)) MB)..."
    $tmp = "$dest.download"
    Invoke-WebRequest -Uri $asset.url -Headers $dl -OutFile $tmp
    $line = ($sumText -split "`n") | Where-Object { ($_ -split '\s+')[1] -eq $asset.name } | Select-Object -First 1
    if (-not $line) { Remove-Item $tmp; throw "SHA256SUMS.txt does not list $($asset.name); refusing to install it unverified" }
    $want = ($line -split '\s+')[0].ToLowerInvariant()
    $got = (Get-FileHash $tmp -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($got -ne $want) { Remove-Item $tmp; throw "SHA-256 mismatch for $($asset.name): got $got, release lists $want" }
    Move-Item -Force $tmp $dest
    Unblock-File $dest
}

$exe = Join-Path $dir 'dsky.exe'
Save-Verified $cli $exe
Copy-Item -Force $exe (Join-Path $dir 'compose.exe')
Write-Host "SHA-256 verified."

if ($app) {
    $appExe = Join-Path $dir 'dsky-app.exe'
    Save-Verified $app $appExe
    $ico = $appExe
    if ($icon) {
        $ico = Join-Path $dir 'dsky.ico'
        Save-Verified $icon $ico
    }
    # The same names `dsky uninstall` removes.
    $shell = New-Object -ComObject WScript.Shell
    foreach ($loc in @(
        (Join-Path ([Environment]::GetFolderPath('Programs')) 'DSKY.lnk'),
        (Join-Path ([Environment]::GetFolderPath('Desktop'))  'DSKY.lnk'))) {
        $lnk = $shell.CreateShortcut($loc)
        $lnk.TargetPath = $appExe
        $lnk.IconLocation = $ico
        $lnk.Description = 'Build and flash bootable OS installers'
        $lnk.WorkingDirectory = $dir
        $lnk.Save()
    }
    Write-Host "Created Start-menu and desktop shortcuts."
}

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not $userPath) { $userPath = '' }
if (($userPath -split ';') -notcontains $dir) {
    [Environment]::SetEnvironmentVariable('Path', ($userPath.TrimEnd(';') + ';' + $dir).TrimStart(';'), 'User')
    Write-Host "Added $dir to your user PATH."
}
$env:Path = "$env:Path;$dir"

# Earlier builds installed under other names (uplink, bootwright) and also
# shipped a compose.exe. If one of those folders sits earlier on PATH,
# `compose` quietly keeps running the old program. Say so; deleting someone's
# files is not an installer's call.
$first = Get-Command compose -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
if ($first -and (Split-Path $first.Source) -ne $dir) {
    Write-Warning "'compose' resolves to $($first.Source), not this install. Remove that older copy or move $dir ahead of it on PATH."
}

Write-Host ""
Write-Host "Installed DSKY $tag to $dir."
Write-Host "Launch DSKY from the Start menu, or from a NEW terminal:"
Write-Host "  dsky serve --open        # the portal"
Write-Host "  compose <recipe>         # one-shot build + flash, inside a workspace"
