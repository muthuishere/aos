<# : batch part — this file is both a .cmd and a PowerShell script
@echo off
rem live-test.cmd — focus-independent feature pass for aos on Windows.
rem   scripts\live-test.cmd [path\to\aos.exe]
rem Asserts only on machine-checkable state (JSON results, WM_GETTEXT / UI
rem Automation read-back, window list IDs, events.json), on monitor 1 only.
rem Prints PASS/FAIL per step; exits non-zero if any step failed.
set "FFDIR=%LOCALAPPDATA%\Microsoft\WinGet\Packages\Gyan.FFmpeg_Microsoft.Winget.Source_8wekyb3d8bbwe\ffmpeg-9.0.2-full_build\bin"
if exist "%FFDIR%\ffmpeg.exe" set "PATH=%FFDIR%;%PATH%"
set AOS_NO_SKILL_AUTOINSTALL=1
powershell -NoProfile -ExecutionPolicy Bypass -Command "& ([scriptblock]::Create((Get-Content -Raw -LiteralPath '%~f0'))) '%~1'"
exit /b %errorlevel%
#>
param([string]$AosBin)
if (-not $AosBin) { $AosBin = "aos" }
$ErrorActionPreference = "Continue"
$Mon = if ($env:AOS_TEST_MONITOR) { [int]$env:AOS_TEST_MONITOR } else { 1 }
$script:Fails = 0; $script:Step = 0
$Work = Join-Path (Get-Location) ("aos-live-" + [guid]::NewGuid().ToString("N").Substring(0, 8))
New-Item -ItemType Directory -Path $Work | Out-Null
$Doc = Join-Path $Work "aos-live-test.txt"; Set-Content -Path $Doc -Value "" -NoNewline
$Out = Join-Path $Work "live.mp4"

function A { & $AosBin @args 2>&1 | Out-String }
function Pass($n) { $script:Step++; "PASS  {0,2}  {1}" -f $script:Step, $n }
function Fail($n, $why) { $script:Step++; $script:Fails++; "FAIL  {0,2}  {1}" -f $script:Step, $n; if ($why) { "          " + ($why.Trim() -replace "`r?`n", " ") } }
function J($raw) { try { $raw | ConvertFrom-Json } catch { $l = ($raw.Trim() -split "`r?`n")[-1]; try { $l | ConvertFrom-Json } catch { $null } } }
function Check($name, $raw, [scriptblock]$ok) {
    $j = J $raw
    $r = $false
    if ($j) { try { $r = [bool](& $ok $j) } catch { $r = $false } }
    if ($r) { Pass $name } else { Fail $name ($raw.Substring(0, [Math]::Min(300, $raw.Length))) }
}
function Inside($r) { $r.X -ge $M.X -and $r.Y -ge $M.Y -and ($r.X + $r.Width) -le ($M.X + $M.W) -and ($r.Y + $r.Height) -le ($M.Y + $M.H) }
function Windows { (J (A window list --json)) | ForEach-Object { $_ } }
function FocusedId { (Windows | Where-Object { $_.Focused } | Select-Object -First 1).ID }

# ---- monitor ----
$M = $null
foreach ($line in ((A display list) -split "`r?`n")) {
    if ($line -match '^\s*(\d+)\s+(\d+)x(\d+)([+-]\d+)([+-]\d+)' -and [int]$Matches[1] -eq $Mon) {
        $M = @{ X = [int]$Matches[4]; Y = [int]$Matches[5]; W = [int]$Matches[2]; H = [int]$Matches[3] }
    }
}
if (-not $M) { "monitor $Mon not found"; exit 2 }
"aos live test on monitor $Mon ($($M.X),$($M.Y),$($M.W),$($M.H)), work dir $Work"

$notepad = $null; $calcWas = @(Get-Process CalculatorApp -ErrorAction SilentlyContinue).Count -gt 0
try {
    # ---- windows this test needs, on the test monitor ----
    $notepad = Start-Process notepad.exe -ArgumentList "`"$Doc`"" -PassThru
    A window wait --title=aos-live-test --timeout=15000 | Out-Null
    Start-Process "calculator:" | Out-Null
    A window wait --title=Calculator --timeout=15000 | Out-Null
    A window move --title=aos-live-test --monitor=$Mon "--at=40,80,900,600" | Out-Null
    A window move --title=Calculator --monitor=$Mon "--at=1000,80,420,640" | Out-Null
    Start-Sleep -Milliseconds 800
    $np = Windows | Where-Object { $_.Title -like "*aos-live-test*" } | Select-Object -First 1
    if ($np -and $np.Monitor -eq $Mon -and (Inside $np.Bounds)) { Pass "Notepad test document is on monitor $Mon" } else { Fail "Notepad on monitor $Mon" ($np | ConvertTo-Json -Compress) }
    $calc = Windows | Where-Object { $_.Title -eq "Calculator" } | Select-Object -First 1
    if ($calc -and $calc.Monitor -eq $Mon) { Pass "Calculator is on monitor $Mon" } else { Fail "Calculator on monitor $Mon" ($calc | ConvertTo-Json -Compress) }

    # ---- recording ----
    $rec = A capture video start smart --monitor=$Mon "--out=$Out" --max=3m --json
    Check "smart recording started on monitor $Mon" $rec { param($j) $j.Mode -eq "smart" -and $j.Capture.X -eq $M.X -and $j.Capture.Width -eq $M.W }

    # ---- 1. focus-free typing, verified by state ----
    $before = FocusedId
    $r = A key type "aos live test" --title=aos-live-test --verify --json
    Check "key type --verify into Notepad (posted, WM_GETTEXT read back)" $r { param($j) $j.Route -eq "post" -and $j.Verified -and $j.Value -like "*aos live test*" -and $j.Window.ID -eq $np.ID }
    $r = A key press enter --title=aos-live-test --json
    Check "key press enter into Notepad (posted)" $r { param($j) $j.Route -eq "post" }
    $r = A key type "second line" --title=aos-live-test --verify --json
    Check "second line verified after enter" $r { param($j) $j.Verified -and (($j.Value -replace "`r`n", "`n" -replace "`r", "`n") -like "*aos live test`nsecond line*") }
    $after = FocusedId
    if ($after -eq $before) { Pass "OS focus unchanged by focus-free input (focused: $after)" } else { Fail "OS focus unchanged" "before=$before after=$after" }
    & $AosBin key type x --app=NoSuchAppAOS --verify *> $null
    if ($LASTEXITCODE -eq 3) { Pass "unknown target exits 3, types nothing" } else { Fail "unknown target exits 3" "rc=$LASTEXITCODE" }
    $r = A key press ctrl+a --title=aos-live-test --json --verify
    if ($LASTEXITCODE -ne 0 -or $r -match '"Route":"focus"') { Pass "modifier chord routes to focus (or is refused) on Windows" } else { Fail "modifier chord routes to focus" $r }

    # ---- 2. Calculator: UI Automation Invoke, no pointer ----
    $cur0 = A mouse position
    $r = A mouse click --title=Calculator --find=Seven --role=button --json
    Check "mouse click --find=Seven used UIA Invoke" $r { param($j) $j.Route -eq "press" -and (Inside $j.Found.Bounds) }
    $cur1 = A mouse position
    if ($cur0.Trim() -eq $cur1.Trim()) { Pass "pointer did not move ($($cur1.Trim()))" } else { Fail "pointer did not move" "$cur0 -> $cur1" }
    Start-Sleep -Milliseconds 500
    $r = A find --title=Calculator --text="Display is 7" --source=accessibility --no-raise --json
    Check "Calculator display reads 7 (UI Automation)" $r { param($j) $j.Label -like "*7*" }

    # ---- 3. overlays land on the recording's monitor ----
    $r = A subtitle show "aos live test: subtitle" --seconds=2 --json
    Check "subtitle (no --monitor) on the recording's monitor" $r { param($j) $j.Monitor -eq $Mon -and (Inside $j.Sign) }
    $r = A draw text "aos live test: label" --duration=1 --json
    Check "draw text (no --monitor) on the recording's monitor" $r { param($j) $j.Monitor -eq $Mon -and (Inside $j.Sign) }
    $r = A draw "--at=300,300" "--text=relative point" --duration=1 --json
    Check "draw --at is relative to the recording's monitor" $r { param($j) $j.Monitor -eq $Mon -and $j.Tip.X -eq ($M.X + 300) -and $j.Tip.Y -eq ($M.Y + 300) }
    $r = A draw --title=Calculator --find=Seven --role=button --style=ring "--text=press 7" --duration=1 --json
    Check "draw --find on Calculator stays on monitor $Mon" $r { param($j) $j.Monitor -eq $Mon -and (Inside $j.Sign) }

    # ---- 4. a move of the target, then window-list IDs confirm a close ----
    A window move --title=aos-live-test --monitor=$Mon "--at=60,100,880,580" | Out-Null
    Start-Sleep -Milliseconds 1200
    $stop = A capture video stop --json
    Check "recording stopped and final cut rendered" $stop { param($j) $j.Final -and $j.Duration -gt 2 }
    $sj = J $stop
    $npId = $np.ID
    # Win11 Notepad hands the file to its own (possibly shared) process, so
    # close the process that owns the listed window, not the launcher.
    Stop-Process -Id $np.PID -Force -ErrorAction SilentlyContinue
    Stop-Process -Id $notepad.Id -Force -ErrorAction SilentlyContinue; $notepad = $null
    Start-Sleep -Milliseconds 800
    if ($npId -and -not (Windows | Where-Object { $_.ID -eq $npId })) { Pass "closed Notepad's ID $npId is gone from window list --json" } else { Fail "closed window's ID gone" $npId }

    # ---- 5. the recording's events ----
    if ($sj -and (Test-Path $sj.Events)) {
        $ev = Get-Content -Raw $sj.Events | ConvertFrom-Json
        if ($ev.capture.X -eq $M.X -and $ev.capture.Width -eq $M.W) { Pass "events: capture area is monitor $Mon" } else { Fail "events: capture area" ($ev.capture | ConvertTo-Json -Compress) }
        $bad = @($ev.events | Where-Object { $_.rect -and $_.kind -ne "cursor" -and -not (Inside $_.rect) })
        if ($bad.Count -eq 0) { Pass "events: every rect is inside monitor $Mon" } else { Fail "events: rects outside monitor $Mon" ($bad | ConvertTo-Json -Compress) }
        $labels = @($ev.events | Where-Object { $_.kind -eq "window" } | ForEach-Object { $_.label } | Sort-Object -Unique)
        $foreign = @($labels | Where-Object { $_ -notin @("Notepad", "ApplicationFrameHost") })
        if ($labels.Count -gt 0 -and $foreign.Count -eq 0) { Pass "events: camera windows are only aos targets ($($labels -join ', '))" } else { Fail "events: camera windows only aos targets" ($labels -join ", ") }
        $cmds = @($ev.events | ForEach-Object { $_.cmd })
        if (($cmds -contains "key type") -and ($cmds -contains "mouse click") -and ($cmds -contains "draw")) { Pass "events: key/click/draw actions were logged" } else { Fail "events: actions logged" ($cmds -join ",") }
        if ($ev.events | Where-Object { $_.kind -eq "window" -and $_.label -eq "Notepad" -and $_.rect.X -eq ($M.X + 60) }) { Pass "events: the target's move was logged" } else { Fail "events: target move logged" "" }
        "final cut: $($sj.Final)"
    } else { Fail "events.json exists" $stop }
}
finally {
    & $AosBin capture video stop --raw *> $null
    & $AosBin draw stop *> $null
    if ($notepad) { Stop-Process -Id $notepad.Id -Force -ErrorAction SilentlyContinue }
    if ($np -and $np.PID) { Stop-Process -Id $np.PID -Force -ErrorAction SilentlyContinue }
    if (-not $calcWas) { Get-Process CalculatorApp -ErrorAction SilentlyContinue | Stop-Process -Force }
}
""
if ($script:Fails -eq 0) { "ALL $($script:Step) STEPS PASSED" } else { "$($script:Fails) OF $($script:Step) STEPS FAILED" }
if ($script:Fails -eq 0) { exit 0 } else { exit 1 }
