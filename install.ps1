#!/usr/bin/env pwsh
# One-line Windows installer for handoff.
#
# Usage: irm https://handoff.gosuper.dev/install.ps1 | iex
#
# Responsibility:
#   - Detect the architecture, fetch the matching zip from the GitHub Release,
#     verify its sha256, install it into %LOCALAPPDATA%\Programs\handoff
#
# Boundaries:
#   - Meant for a machine that has no handoff yet; use `handoff upgrade` afterwards
#   - Does NOT touch PATH, does NOT install a service, does NOT elevate
#     (each boundary matches install.sh exactly)
#   - On Windows handoff can only act as a coordinator: agentd's process host
#     is not implemented for non-unix platforms yet (backlog B37), so the
#     dispatch target must be a macOS or Linux executor machine
#
# Environment variables:
#   HANDOFF_INSTALL_DIR  override the install directory
#   HANDOFF_INSTALL_LIB  set to 1 to only define functions, skipping the main
#                        flow (used by install_test.ps1)
#
# Compatibility: must work on both Windows' built-in PowerShell 5.1 and PowerShell 7.
#
# ---------------------------------------------------------------------------
# THIS FILE MUST STAY PURE ASCII, AND MUST NOT CARRY A UTF-8 BOM.
# Do not "fix" it back to Chinese comments; both constraints are load-bearing,
# and they come from the two ways this file is consumed:
#
#   1. `irm ... | iex` (the documented one-liner, and the path essentially every
#      user takes). `irm` hands `iex` a *string*. PowerShell 5.1 does not treat
#      U+FEFF as whitespace, so a BOM becomes part of the very first token:
#      the script dies on line 1 with "cannot recognize ?# as a cmdlet" no
#      matter what that line says -- comment, blank line, anything.
#      Measured 2026-08-13 on Windows Server 2025, PowerShell 5.1.26100, zh-CN.
#
#   2. Saved to disk and run as a .ps1. Without a BOM, PowerShell 5.1 decodes
#      the file using the system ANSI code page. On Chinese Windows that is
#      cp936/GBK, whose lead bytes swallow the ASCII character that follows,
#      turning the script into a syntax error.
#
# A BOM fixes (2) and breaks (1); no BOM fixes (1) and breaks (2) -- unless the
# file contains no non-ASCII bytes at all, which decodes identically under
# UTF-8, cp936 and cp1252. Hence: ASCII only, no BOM. Enforced by
# TestInstallPs1IsBOMFreeASCII in release_workflow_test.go.
#
# install_test.ps1 is a different case: it is only ever run from disk, never
# piped into iex, so it keeps its BOM and its Chinese text.
# ---------------------------------------------------------------------------
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$Repo = 'Xsxdot/handoff'

# Write-Log writes to stderr; stdout is left for content a pipe may consume.
function Write-Log([string]$Message) {
    [Console]::Error.WriteLine($Message)
}

# Stop-Install reports why we are giving up, then throws.
#
# Every failure branch must exit through here: this single line is all the user
# gets to see when the script dies, and "install failed" without context leaves
# them guessing between network, permissions and architecture.
function Stop-Install([string]$Message) {
    throw "handoff install failed: $Message"
}

# Get-HandoffArch normalizes the processor architecture to the Release asset name.
#
# Returns: amd64 or arm64
#
# Note: anything outside the build matrix is a hard error. Silently installing a
# binary that cannot run is far worse -- the symptom shows up later, at run time,
# with no hint of the cause.
function Get-HandoffArch {
    switch ($env:PROCESSOR_ARCHITECTURE) {
        'AMD64' { return 'amd64' }
        'ARM64' { return 'arm64' }
        default { Stop-Install "unsupported architecture $($env:PROCESSOR_ARCHITECTURE) (only AMD64/ARM64)" }
    }
}

# Get-HandoffInstallDir resolves the install directory.
#
# Defaults to %LOCALAPPDATA%\Programs\handoff -- the per-user install convention
# on Windows, requiring no administrator rights. install.sh's ~/.local/bin means
# nothing to any tool on Windows.
function Get-HandoffInstallDir {
    if ($env:HANDOFF_INSTALL_DIR) { return $env:HANDOFF_INSTALL_DIR }
    return (Join-Path $env:LOCALAPPDATA 'Programs\handoff')
}

# ConvertTo-HandoffText turns an Invoke-WebRequest .Content into a string.
#
# Params:
#   - Content: whatever .Content gave us (string or byte[])
#
# Why this exists: GitHub serves release assets as application/octet-stream,
# including checksums.txt. For a non-text content type Invoke-WebRequest hands
# back a **byte[]**, not a string -- on PowerShell 5.1 and 7 alike. Splitting a
# byte[] on newlines yields one useless object, so the checksum lookup found no
# entry and every single install died with "checksums.txt has no entry for ...".
# Measured 2026-08-13 on a real box; CI never caught it because install_test.ps1
# only exercises the pure functions and never goes near the network.
function ConvertTo-HandoffText($Content) {
    if ($Content -is [byte[]]) {
        return [System.Text.Encoding]::UTF8.GetString($Content)
    }
    return [string]$Content
}

# Get-LatestTag resolves the releases/latest redirect and takes the newest tag.
#
# Returns: something like v0.1.0
#
# Why not api.github.com: anonymous API calls are rate-limited to 60/hour/IP, and
# the install path should not be affected by that. Redirects are not rate-limited.
#
# Why two ways of reading the final URL: on PowerShell 5.1 BaseResponse is an
# HttpWebResponse (use .ResponseUri); on 7 it is an HttpResponseMessage (use
# .RequestMessage.RequestUri). Writing only the 7 form would make the script die
# on its first step on the vast majority of Windows machines.
function Get-LatestTag {
    $url = "https://github.com/$Repo/releases/latest"
    try {
        $resp = Invoke-WebRequest -Uri $url -UseBasicParsing -MaximumRedirection 10
    } catch {
        Stop-Install "cannot resolve the latest version: github.com unreachable ($($_.Exception.Message))"
    }
    $final = $null
    if ($resp.BaseResponse.PSObject.Properties.Name -contains 'ResponseUri') {
        $final = $resp.BaseResponse.ResponseUri.AbsoluteUri
    } elseif ($resp.BaseResponse.PSObject.Properties.Name -contains 'RequestMessage') {
        $final = $resp.BaseResponse.RequestMessage.RequestUri.AbsoluteUri
    }
    if (-not $final) { Stop-Install 'cannot resolve the latest version: no final URL in the response' }
    $tag = $final.Split('/')[-1]
    # With no releases at all, GitHub redirects to .../releases and the last
    # segment is not a version number
    if ($tag -notmatch '^v') { Stop-Install "cannot resolve the latest version: $Repo has no releases yet (redirected to $final)" }
    return $tag
}

# Test-HandoffChecksum compares a file's sha256 against checksums.txt.
#
# Params:
#   - Path: the file to verify
#   - ChecksumsText: the full text of checksums.txt
#   - Name: the bare asset file name
#
# Returns: $true when it matches; a missing entry or a mismatch throws (it never
# returns $false -- a caller who forgets to check the return value and installs a
# corrupt package is the worst failure mode available here)
function Test-HandoffChecksum([string]$Path, [string]$ChecksumsText, [string]$Name) {
    $want = $null
    foreach ($line in $ChecksumsText -split "`n") {
        $f = $line.Trim() -split '\s+'
        if ($f.Count -ne 2) { continue }
        # sha256sum prefixes the name with * in binary mode; tolerate it
        if ($f[1].TrimStart('*') -eq $Name) { $want = $f[0]; break }
    }
    if (-not $want) { Stop-Install "checksums.txt has no entry for $Name" }
    $got = (Get-FileHash -Path $Path -Algorithm SHA256).Hash.ToLower()
    if ($got -ne $want.ToLower()) {
        Stop-Install "checksum mismatch: want $want, got $got. Not installing; the download has been removed"
    }
    return $true
}

# Write-NextSteps prints what to do once the binary is in place.
function Write-NextSteps([string]$Dir) {
    Write-Log ''
    Write-Log 'Next     handoff init'
    Write-Log '         On Windows handoff can only be a coordinator; init walks you'
    Write-Log '         through pairing a remote executor machine.'
    Write-Log '         agentd does not run on Windows (backlog B37), so this machine'
    Write-Log '         cannot be an executor.'
    if (($env:PATH -split ';') -notcontains $Dir) {
        Write-Log ''
        Write-Log "Note: $Dir is not on your PATH. Run this once in PowerShell to add it:"
        Write-Log "  [Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path','User') + ';$Dir', 'User')"
        Write-Log '(this script does not modify your PATH)'
    }
}

# Invoke-Main is the install flow.
function Invoke-Main {
    $arch = Get-HandoffArch
    $tag = Get-LatestTag
    $dir = Get-HandoffInstallDir
    $zip = "handoff_${tag}_windows_${arch}.zip"
    $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("handoff-install-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp -Force | Out-Null
    try {
        Write-Log "handoff $tag  windows_$arch"
        $zipPath = Join-Path $tmp $zip
        Invoke-WebRequest -Uri "https://github.com/$Repo/releases/download/$tag/$zip" -OutFile $zipPath -UseBasicParsing
        $sums = ConvertTo-HandoffText (Invoke-WebRequest -Uri "https://github.com/$Repo/releases/download/$tag/checksums.txt" -UseBasicParsing).Content
        Test-HandoffChecksum -Path $zipPath -ChecksumsText $sums -Name $zip | Out-Null

        Expand-Archive -Path $zipPath -DestinationPath $tmp -Force
        $exe = Join-Path $tmp 'handoff.exe'
        if (-not (Test-Path $exe)) { Stop-Install 'the package contains no handoff.exe' }

        New-Item -ItemType Directory -Path $dir -Force | Out-Null
        $dest = Join-Path $dir 'handoff.exe'
        Copy-Item -Path $exe -Destination $dest -Force
        Write-Log "installed $dest  $tag"

        # Install the skill for every agent on this machine. This must invoke the
        # binary we just installed -- the skill is embedded in it, so calling an
        # older one installs an older skill.
        # A failure here is not an install failure: the binary is in place, and a
        # missing skill does not affect the CLI.
        try {
            & $dest skill install
        } catch {
            Write-Log "Note: skill install failed; you can run `"$dest`" skill install later"
        }

        Write-NextSteps -Dir $dir
    } finally {
        Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
}

# When dot-sourced by install_test.ps1, only define functions
if ($env:HANDOFF_INSTALL_LIB -ne '1') { Invoke-Main }
