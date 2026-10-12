@echo off
rem Install aos on Windows from a GitHub release, a local folder, a mirror URL,
rem or a single aos.exe. No Go toolchain, no administrator.
rem
rem   curl -fsSL -o install.cmd https://raw.githubusercontent.com/muthuishere/aos/main/install.cmd && install.cmd
rem   install.cmd /version v0.6.0
rem   install.cmd /from C:\path\to\release-folder      (goreleaser archives + checksums.txt)
rem   install.cmd /from https://mirror.example/aos/v0.6.0/
rem   install.cmd /from .\aos_0.5.0_windows_arm64.zip     (one release archive: unpacked)
rem   install.cmd /from .\aos.exe                       (copied in as-is)
rem   install.cmd /dir %USERPROFILE%\bin
rem
rem --from / --version / --dir work too. Environment equivalents: AOS_FROM,
rem AOS_VERSION, AOS_INSTALL_DIR. Default dir: %LOCALAPPDATA%\Programs\aos,
rem added to your user PATH.
rem
rem A .cmd rather than a .ps1 on purpose: PowerShell's execution policy blocks
rem unsigned script FILES by default, but not -Command, so the PowerShell below
rem is read out of this file and run that way.
setlocal
set "AOS_INSTALL_ARGS=%*"
set "AOS_INSTALL_SELF=%~f0"
powershell -NoProfile -ExecutionPolicy Bypass -Command "$s = Get-Content -Raw -LiteralPath $env:AOS_INSTALL_SELF; $m = '#' + '#AOS-POWERSHELL##'; Invoke-Expression ($s.Substring($s.IndexOf($m) + $m.Length))"
exit /b %ERRORLEVEL%

##AOS-POWERSHELL##
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$repo = 'muthuishere/aos'
$from = if ($env:AOS_FROM) { $env:AOS_FROM } else { $env:AOS_DOWNLOAD_BASE }
$version = $env:AOS_VERSION
$dir = if ($env:AOS_INSTALL_DIR) { $env:AOS_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\aos' }

function Fail($msg) { [Console]::Error.WriteLine("install: $msg"); exit 1 }

# Split the argument string the way cmd passed it, honouring quotes.
$argv = @([regex]::Matches("$env:AOS_INSTALL_ARGS", '"([^"]*)"|(\S+)') | ForEach-Object {
    if ($_.Groups[1].Success) { $_.Groups[1].Value } else { $_.Groups[2].Value } })
for ($i = 0; $i -lt $argv.Count; $i++) {
    $a = $argv[$i]
    $name = $a -replace '^(--|/)', ''
    $val = $null
    if ($name -match '^(\w+)[=:](.*)$') { $name = $Matches[1]; $val = $Matches[2] }
    if ($name -in @('from', 'version', 'dir')) {
        if ($null -eq $val) { $i++; if ($i -ge $argv.Count) { Fail "$a needs a value" }; $val = $argv[$i] }
        Set-Variable -Name $name -Value $val
    } elseif ($name -in @('h', 'help', '?')) {
        Write-Host 'install.cmd [/from <folder|url|aos.exe>] [/version vX.Y.Z] [/dir <path>]'; exit 0
    } else { Fail "unknown argument: $a" }
}

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64' -or $env:PROCESSOR_ARCHITEW6432 -eq 'ARM64') { 'arm64' } else { 'amd64' }
$tmp = Join-Path ([IO.Path]::GetTempPath()) ('aos-install-' + [guid]::NewGuid())
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$target = Join-Path $dir 'aos.exe'

try {
    if ($from -and (Test-Path -LiteralPath $from -PathType Leaf) -and $from -like '*.zip') {
        Write-Host "==> Installing from the archive $from"
        $tmpa = Join-Path ([IO.Path]::GetTempPath()) ("aos-" + [guid]::NewGuid())
        Expand-Archive -LiteralPath $from -DestinationPath $tmpa -Force
        $exe = Join-Path $tmpa 'aos.exe'
        if (-not (Test-Path -LiteralPath $exe)) { Fail "$from does not contain aos.exe" }
        Copy-Item -LiteralPath $exe -Destination $target -Force
        Remove-Item -Recurse -Force $tmpa
    } elseif ($from -and (Test-Path -LiteralPath $from -PathType Leaf)) {
        Write-Host "==> Installing the executable $from"
        Copy-Item -LiteralPath $from -Destination $target -Force
    } else {
        $isUrl = $false
        if (-not $from) {
            if (-not $version) {
                $version = (Invoke-RestMethod -UseBasicParsing "https://api.github.com/repos/$repo/releases/latest").tag_name
                if (-not $version) { Fail "could not resolve the latest release of $repo" }
            }
            if ($version -notmatch '^v') { $version = "v$version" }
            $base = "https://github.com/$repo/releases/download/$version"; $isUrl = $true
        } else {
            $base = $from.TrimEnd('/', '\')
            $isUrl = $base -match '^https?://'
            if (-not $isUrl -and -not (Test-Path -LiteralPath $base -PathType Container)) {
                Fail "/from $from is not a folder, an executable, or an http(s) URL"
            }
        }
        Write-Host "==> Installing aos for windows/$arch from $base"

        function Fetch($name, $dest) {
            try {
                if ($isUrl) { Invoke-WebRequest -UseBasicParsing -Uri "$base/$name" -OutFile $dest }
                else { Copy-Item -LiteralPath (Join-Path $base $name) -Destination $dest -ErrorAction Stop }
                return $true
            } catch { return $false }
        }

        $sumsFile = Join-Path $tmp 'checksums.txt'
        $sums = @{}
        if (Fetch 'checksums.txt' $sumsFile) {
            Get-Content $sumsFile | ForEach-Object { $p = $_ -split '\s+', 2; if ($p.Count -eq 2) { $sums[$p[1].Trim()] = $p[0] } }
        }
        $pattern = "^aos_.*_windows_$arch\.zip$"
        $archive = $null
        if ($sums.Count -gt 0) {
            $names = @($sums.Keys | Where-Object { $_ -match $pattern })
            if ($version) { $names = @($names | Where-Object { $_ -like "*_$($version.TrimStart('v'))_*" }) }
            $archive = $names | Select-Object -First 1
        } elseif ($version) {
            $archive = "aos_$($version.TrimStart('v'))_windows_$arch.zip"
        } elseif (-not $isUrl) {
            $archive = Get-ChildItem -LiteralPath $base -File | Where-Object { $_.Name -match $pattern } | Select-Object -First 1 -ExpandProperty Name
        } else { Fail "$base has no checksums.txt to find the archive by; pass /version" }
        if (-not $archive) { Fail "no aos archive for windows/$arch at $base" }

        $zip = Join-Path $tmp $archive
        if (-not (Fetch $archive $zip)) { Fail "download failed: $base/$archive" }
        if ($sums.Count -gt 0) {
            $got = (Get-FileHash -Algorithm SHA256 -LiteralPath $zip).Hash.ToLower()
            if ($sums[$archive] -ne $got) { Fail "checksum mismatch for $archive -- refusing to install" }
            Write-Host '    sha256 verified'
        } else {
            Write-Warning "$base has no checksums.txt -- installing $archive WITHOUT verifying it"
        }
        Expand-Archive -LiteralPath $zip -DestinationPath (Join-Path $tmp 'x') -Force
        $exe = Join-Path $tmp 'x\aos.exe'
        if (-not (Test-Path -LiteralPath $exe)) { Fail "$archive does not contain aos.exe" }
        Copy-Item -LiteralPath $exe -Destination $target -Force
    }
} finally {
    if ($tmp -and (Test-Path -LiteralPath $tmp)) { Remove-Item -LiteralPath $tmp -Recurse -Force }
}

Write-Host "    $target"
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not (($userPath -split ';') -contains $dir)) {
    [Environment]::SetEnvironmentVariable('Path', ($(if ($userPath) { "$userPath;$dir" } else { $dir })), 'User')
    Write-Host "    added $dir to your user PATH (open a new terminal to pick it up)"
}
$env:Path = "$dir;$env:Path"
# Running it once also installs the bundled agent skill.
& $target --version
exit $LASTEXITCODE
