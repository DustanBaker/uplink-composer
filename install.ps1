# The Uplink CompOSer — no-admin installer for Windows.
#
#   irm https://raw.githubusercontent.com/DustanBaker/the-composer/main/install.ps1 | iex
#
# Downloads the latest release binary for this machine into
# %LOCALAPPDATA%\Programs\composer, verifies its SHA-256 against the
# release's SHA256SUMS.txt, and adds that folder to the USER PATH — no
# administrator rights needed. Installs both `composer` and the `compose`
# alias. Set GITHUB_TOKEN to install from a private repository.
$ErrorActionPreference = 'Stop'
$repo = 'DustanBaker/the-composer'

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$headers = @{ 'User-Agent' = 'the-composer-installer' }
if ($env:GITHUB_TOKEN) { $headers['Authorization'] = "Bearer $env:GITHUB_TOKEN" }

$rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -Headers $headers
$asset = $rel.assets | Where-Object { $_.name -like "composer-*-windows-$arch.exe" } | Select-Object -First 1
$sums  = $rel.assets | Where-Object { $_.name -eq 'SHA256SUMS.txt' } | Select-Object -First 1
if (-not $asset) { throw "release $($rel.tag_name) has no windows-$arch build" }

$dir = Join-Path $env:LOCALAPPDATA 'Programs\composer'
New-Item -ItemType Directory -Force $dir | Out-Null
$exe = Join-Path $dir 'composer.exe'
$dl = @{ 'User-Agent' = 'the-composer-installer'; 'Accept' = 'application/octet-stream' }
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

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not $userPath) { $userPath = '' }
if (($userPath -split ';') -notcontains $dir) {
    [Environment]::SetEnvironmentVariable('Path', ($userPath.TrimEnd(';') + ';' + $dir).TrimStart(';'), 'User')
    Write-Host "Added $dir to your user PATH."
}
$env:Path = "$env:Path;$dir"

Write-Host ""
Write-Host "Installed The Uplink CompOSer $($rel.tag_name) to $dir (composer, compose)."
Write-Host "Open a NEW terminal, cd into a workspace, and run:  compose <recipe>"
Write-Host "No workspace yet?  composer init --org `"Your Org`" my-workspace"
