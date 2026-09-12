# The Uplink CompOSer — no-admin installer for Windows.
#
#   irm https://raw.githubusercontent.com/DustanBaker/uplink-composer/main/install.ps1 | iex
#
# Downloads the latest release binary for this machine into
# %LOCALAPPDATA%\Programs\uplink, verifies its SHA-256 against the
# release's SHA256SUMS.txt, and adds that folder to the USER PATH — no
# administrator rights needed. Installs both `uplink` and the `compose`
# alias. Set GITHUB_TOKEN to install from a private repository.
$ErrorActionPreference = 'Stop'
$repo = 'DustanBaker/uplink-composer'

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$headers = @{ 'User-Agent' = 'uplink-composer-installer' }
if ($env:GITHUB_TOKEN) { $headers['Authorization'] = "Bearer $env:GITHUB_TOKEN" }

$rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -Headers $headers
$asset = $rel.assets | Where-Object { $_.name -like "uplink-*-windows-$arch.exe" } | Select-Object -First 1
$sums  = $rel.assets | Where-Object { $_.name -eq 'SHA256SUMS.txt' } | Select-Object -First 1
if (-not $asset) { throw "release $($rel.tag_name) has no windows-$arch build" }

$dir = Join-Path $env:LOCALAPPDATA 'Programs\uplink'
New-Item -ItemType Directory -Force $dir | Out-Null
$exe = Join-Path $dir 'uplink.exe'
$dl = @{ 'User-Agent' = 'uplink-composer-installer'; 'Accept' = 'application/octet-stream' }
if ($env:GITHUB_TOKEN) { $dl['Authorization'] = "Bearer $env:GITHUB_TOKEN" }

Write-Host "Downloading $($asset.name) ($([math]::Round($asset.size / 1MB, 1)) MB)..."
$ProgressPreference = 'SilentlyContinue'
Invoke-WebRequest -Uri $asset.url -Headers $dl -OutFile "$exe.download"

if ($sums) {
    $sumText = Invoke-RestMethod -Uri $sums.url -Headers $dl
    $line = ($sumText -split "`n") | Where-Object { $_ -match [regex]::Escape($asset.name) } | Select-Object -First 1
    if ($line) {
        $want = ($line -split '\s+')[0].ToLowerInvariant()
        $got = (Get-FileHash "$exe.download" -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($got -ne $want) { Remove-Item "$exe.download"; throw "SHA-256 mismatch for $($asset.name): got $got, release lists $want" }
        Write-Host "SHA-256 verified."
    }
}
Move-Item -Force "$exe.download" $exe
Unblock-File $exe
Copy-Item -Force $exe (Join-Path $dir 'compose.exe')

# Windowless launcher for the click-to-launch app + its icon (best effort).
$appAsset = $rel.assets | Where-Object { $_.name -like "uplink-app-*-windows-$arch.exe" } | Select-Object -First 1
$appExe = Join-Path $dir 'uplink-app.exe'
$ico = Join-Path $dir 'uplink.ico'
if ($appAsset) {
    Invoke-WebRequest -Uri $appAsset.url -Headers $dl -OutFile $appExe
    Unblock-File $appExe
    try { Invoke-WebRequest -Uri "https://raw.githubusercontent.com/$repo/main/uplink.ico" -Headers @{ 'User-Agent' = 'uplink-composer-installer' } -OutFile $ico } catch {}

    $target = $appExe
    if (-not (Test-Path $ico)) { $ico = $appExe }
    $shell = New-Object -ComObject WScript.Shell
    foreach ($loc in @(
        (Join-Path ([Environment]::GetFolderPath('Programs')) 'The Uplink CompOSer.lnk'),
        (Join-Path ([Environment]::GetFolderPath('Desktop'))  'The Uplink CompOSer.lnk'))) {
        $lnk = $shell.CreateShortcut($loc)
        $lnk.TargetPath = $target
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

Write-Host ""
Write-Host "Installed The Uplink CompOSer $($rel.tag_name) to $dir."
Write-Host "Launch the app from the Start menu (The Uplink CompOSer), or from a NEW terminal:"
Write-Host "  uplink serve --open      # the web portal"
Write-Host "  compose <recipe>         # one-shot build + flash, inside a workspace"
