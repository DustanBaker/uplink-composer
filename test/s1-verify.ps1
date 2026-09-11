# S1 native verification (run elevated): mounts the s1gen VHD with Windows'
# own FAT driver, runs chkdsk, and hash-compares every file against the
# manifest. Writes s1-result.txt (PASS/FAIL + detail) next to the VHD.
#
#   go run ./test/s1gen <dir>
#   Start-Process powershell -Verb RunAs -ArgumentList '-ExecutionPolicy','Bypass','-File','test\s1-verify.ps1','-Dir','<dir>'
param(
    [Parameter(Mandatory = $true)][string]$Dir
)
$ErrorActionPreference = 'Stop'
$result = Join-Path $Dir 's1-result.txt'
$lines = @()
$failed = $false

function Note([string]$msg) {
    $script:lines += $msg
    Write-Host $msg
}

try {
    $vhd = (Resolve-Path (Join-Path $Dir 's1.vhd')).Path
    $manifest = Get-Content (Join-Path $Dir 'manifest.json') -Raw | ConvertFrom-Json

    Note "Mounting $vhd"
    Mount-DiskImage -ImagePath $vhd -PassThru | Out-Null
    Start-Sleep -Seconds 2
    $disk = Get-DiskImage -ImagePath $vhd | Get-Disk
    $part = $disk | Get-Partition | Where-Object { $_.DriveLetter }
    if (-not $part) {
        # No drive letter auto-assigned; assign one.
        $part = $disk | Get-Partition | Select-Object -First 1
        $part | Add-PartitionAccessPath -AssignDriveLetter
        $part = $disk | Get-Partition | Where-Object { $_.DriveLetter }
    }
    $letter = $part.DriveLetter
    Note "Mounted as ${letter}:"

    $vol = Get-Volume -DriveLetter $letter
    Note ("Volume: label='{0}' fs={1} size={2:N0} free={3:N0}" -f $vol.FileSystemLabel, $vol.FileSystem, $vol.Size, $vol.SizeRemaining)
    if ($vol.FileSystem -ne 'FAT32') { $failed = $true; Note "FAIL: filesystem is $($vol.FileSystem), expected FAT32" }
    if ($vol.FileSystemLabel -ne 'ESD-USB') { $failed = $true; Note "FAIL: label is '$($vol.FileSystemLabel)', expected 'ESD-USB'" }

    Note "Running chkdsk ${letter}:"
    $chkdsk = & "$env:SystemRoot\System32\chkdsk.exe" "${letter}:"
    $chkdskText = $chkdsk -join "`n"
    $lines += $chkdskText
    if ($LASTEXITCODE -ne 0) { $failed = $true; Note "FAIL: chkdsk exit code $LASTEXITCODE" }
    else { Note "chkdsk clean (exit 0)" }

    Note "Hash-comparing files against manifest"
    $bad = 0; $checked = 0
    foreach ($p in $manifest.PSObject.Properties) {
        $rel = $p.Name
        $want = $p.Value
        $full = Join-Path "${letter}:\" ($rel -replace '/', '\')
        if (-not (Test-Path -LiteralPath $full)) {
            $failed = $true; $bad++
            Note "FAIL missing: $rel"
            continue
        }
        $got = (Get-FileHash -LiteralPath $full -Algorithm SHA256).Hash.ToLowerInvariant()
        $checked++
        if ($got -ne $want) {
            $failed = $true; $bad++
            Note "FAIL hash mismatch: $rel"
        }
    }
    Note "Checked $checked files, $bad bad"

    # Exclude System Volume Information — Windows creates it on mount.
    $onDisk = (Get-ChildItem -LiteralPath "${letter}:\" -Recurse -File -Force |
        Where-Object { $_.FullName -notmatch 'System Volume Information' } | Measure-Object).Count
    $expected = ($manifest.PSObject.Properties | Measure-Object).Count
    if ($onDisk -ne $expected) { $failed = $true; Note "FAIL: $onDisk files on volume, manifest has $expected" }
    else { Note "File count matches manifest ($onDisk)" }
}
catch {
    $failed = $true
    Note ("EXCEPTION: " + $_.Exception.Message)
}
finally {
    try { Dismount-DiskImage -ImagePath $vhd | Out-Null; Note "Dismounted" } catch {}
    $verdict = if ($failed) { 'FAIL' } else { 'PASS' }
    @("S1 native verification: $verdict", "") + $lines | Set-Content -Encoding utf8 $result
    Write-Host "S1 native verification: $verdict (details: $result)"
}
