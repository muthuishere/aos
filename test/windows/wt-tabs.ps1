# Windows Terminal tabs, focus-free, plus Calculator's first press and a Paint
# background-drawing spike. Opens two named WT windows (A: 1 tab, B: 2 tabs),
# puts an existing Notepad window in front (nothing is typed into it), drives
# window B's tab 2 through aos, and checks the foreground never changes.
# Closes only what it opened.
#   cmd /c powershell -NoProfile -ExecutionPolicy Bypass -File wt-tabs.ps1
$ErrorActionPreference = 'Continue'
$aos = Join-Path $PSScriptRoot 'aos.exe'
$env:AOS_NO_SKILL_AUTOINSTALL = '1'; $env:AOS_CALLER = 'wt-tabs.ps1'
Add-Type @'
using System; using System.Runtime.InteropServices; using System.Text;
public static class W {
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr h, StringBuilder s, int n);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h, uint m, IntPtr w, IntPtr l);
  [DllImport("user32.dll")] public static extern bool EnumChildWindows(IntPtr p, EnumProc f, IntPtr l);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetClassName(IntPtr h, StringBuilder s, int n);
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  public delegate bool EnumProc(IntPtr h, IntPtr l);
  public struct RECT { public int L, T, R, B; }
  public static string Fg() { var s = new StringBuilder(256); GetWindowText(GetForegroundWindow(), s, 256); return GetForegroundWindow() + " '" + s + "'"; }
}
'@
$results = @(); $n = 0
function Shot($name) { $p = Join-Path $PSScriptRoot "shot-$name.png"; & $aos capture screenshot fullscreen "--out=$p" 2>&1 | Out-Null }
function Check($what, [scriptblock]$act, [scriptblock]$landed) {
  $script:n++; $id = '{0:d2}-{1}' -f $script:n, (($what -replace '[^A-Za-z0-9]+', '-').Trim('-').ToLower())
  Shot "$id-before"; $fg0 = [W]::Fg()
  $out = & $act 2>&1 | Out-String
  Start-Sleep -Milliseconds 1500
  $fg1 = [W]::Fg(); Shot "$id-after"
  $ok = [bool](& $landed $out); $kept = $fg0 -eq $fg1
  $script:results += [pscustomobject]@{ Step = $what; Result = $(if ($ok -and $kept) { 'PASS' } else { 'FAIL' }); Landed = $ok; ForegroundKept = $kept; Shots = $id }
  Write-Host ("[{0}] {1}  landed={2} fgKept={3}" -f $(if ($ok -and $kept) { 'PASS' } else { 'FAIL' }), $what, $ok, $kept)
  Write-Host ("    " + (($out.Trim() -replace "`r?`n", ' | ')))
  if (-not $kept) { Write-Host "    foreground: $fg0 -> $fg1" }
}
# Leftovers of an earlier run (tags passed as arguments) are closed first.
foreach ($old in $args) { foreach ($p in 'A1', 'B1', 'B2') { & $aos tab close --title="$p-$old" --app=WindowsTerminal 2>&1 | ForEach-Object { Write-Host "  cleanup: $_" } } }
$tag = 't' + (Get-Random -Maximum 9999)
$A1 = "A1-$tag"; $B1 = "B1-$tag"; $B2 = "B2-$tag"
& $aos tab open --app=WindowsTerminal --wt-window="aosA-$tag" --title=$A1 --cwd=$env:TEMP | Out-Null
Start-Sleep 2
& $aos tab open --app=WindowsTerminal --wt-window="aosB-$tag" --title=$B1 --cwd=$env:USERPROFILE | Out-Null
Start-Sleep 2
& $aos tab open --app=WindowsTerminal --wt-window="aosB-$tag" --title=$B2 --cwd=C:\Windows | Out-Null
Start-Sleep 3
# Front: an existing Notepad window (not edited), else Explorer.
$front = (& $aos window list --json | ConvertFrom-Json) | Where-Object { $_.App -eq 'Notepad' } | Select-Object -First 1
if ($front) { [void][W]::SetForegroundWindow([IntPtr][int64]$front.ID) }
Start-Sleep 1
Write-Host "front: $([W]::Fg())"
Write-Host '--- aos tab list --app=WindowsTerminal'
& $aos tab list --app=WindowsTerminal --json | ConvertFrom-Json | ForEach-Object { Write-Host ("  {0}  idx={1} active={2} pid={3} match={4} cwd={5}  '{6}'" -f $_.id, $_.index, $_.active, $_.pid, $_.match, $_.cwd, $_.title) }

Check "tab list has our 3 tabs with pids" { & $aos tab list --app=WindowsTerminal --json } { param($o) $j = $o | ConvertFrom-Json; @($j | Where-Object { $_.title -like "*$tag" -and $_.pid -gt 0 }).Count -eq 3 }
Check "tab type into window B tab 2 (background, not selected)" { & $aos tab type --title=$B2 --text='echo hello-tab2' --enter } { param($o) $o -match 'console input' }
Start-Sleep 1
Check "tab read window B tab 2" { & $aos tab read --title=$B2 } { param($o) $o -match 'hello-tab2' -and $o -match 'C:\\Windows' }
Check "key type --tab into window A" { & $aos key type --tab=$A1 "echo hello-from-key-type`n" } { param($o) $o -match 'console input' }
Start-Sleep 1
Check "tab read window A" { & $aos tab read --title=$A1 } { param($o) $o -match 'hello-from-key-type' }
Check "tab send --key=ctrl+c to B2" { $id = ((& $aos tab list --app=WindowsTerminal --json | ConvertFrom-Json) | Where-Object { $_.title -eq $B2 }).id; & $aos tab send --id=$id --key=ctrl+c } { param($o) $o -notmatch 'error' }
Check "tab focus B1 in place (no raise)" { & $aos tab focus --title=$B1 } { param($o) ((& $aos tab list --app=WindowsTerminal --json | ConvertFrom-Json) | Where-Object { $_.title -eq $B1 }).active }

# Calculator: first press must not bring it to the front.
$hadCalc = @(Get-Process CalculatorApp -ErrorAction SilentlyContinue).Count -gt 0
if (-not $hadCalc) {
  Start-Process calc.exe; Start-Sleep 5
  if ($front) { [void][W]::SetForegroundWindow([IntPtr][int64]$front.ID) }; Start-Sleep 1
  Check "calculator first press 7 (UWP)" { & $aos mouse click --title=Calculator --find=Seven } { param($o) $o -match 'press' }
  Check "calculator press + 2 =" { & $aos mouse click --title=Calculator --find=Plus; & $aos mouse click --title=Calculator --find=Two; & $aos mouse click --title=Calculator --find=Equals } { param($o) (& $aos window text --title=Calculator --source=accessibility | Out-String) -match 'Display is 9' }
  Get-Process CalculatorApp -ErrorAction SilentlyContinue | Stop-Process -Force
}

# Paint spike: post a mouse drag to the canvas child while Paint is behind.
$hadPaint = @(Get-Process mspaint -ErrorAction SilentlyContinue).Count -gt 0
if (-not $hadPaint) {
  $paint = Start-Process mspaint.exe -PassThru; Start-Sleep 5
  if ($front) { [void][W]::SetForegroundWindow([IntPtr][int64]$front.ID) }; Start-Sleep 1
  $pw = (& $aos window list --json | ConvertFrom-Json) | Where-Object { $_.Title -like '*Paint*' } | Select-Object -First 1
  if ($pw) {
    $kids = New-Object System.Collections.ArrayList
    [void][W]::EnumChildWindows([IntPtr][int64]$pw.ID, { param($h, $l) $s = New-Object Text.StringBuilder 256; [void][W]::GetClassName($h, $s, 256); $r = New-Object W+RECT; [void][W]::GetWindowRect($h, [ref]$r); [void]$kids.Add([pscustomobject]@{ H = $h; Cls = $s.ToString(); Area = ($r.R - $r.L) * ($r.B - $r.T) }); $true }, [IntPtr]::Zero)
    Write-Host "  paint children: $(($kids | Sort-Object Area -Descending | Select-Object -First 6 | ForEach-Object { "$($_.Cls)($($_.Area))" }) -join ', ')"
    $canvas = $kids | Sort-Object Area -Descending | Select-Object -First 1
    Check "paint: posted mouse drag on the canvas child (background)" {
      $mk = { param($x, $y) [IntPtr](($y -shl 16) -bor $x) }
      [void][W]::PostMessage($canvas.H, 0x0201, [IntPtr]1, (& $mk 100 100))
      for ($i = 1; $i -le 20; $i++) { [void][W]::PostMessage($canvas.H, 0x0200, [IntPtr]1, (& $mk (100 + $i * 15) (100 + $i * 8))); Start-Sleep -Milliseconds 15 }
      [void][W]::PostMessage($canvas.H, 0x0202, [IntPtr]0, (& $mk 400 260))
      "posted to $($canvas.Cls)"
    } { param($o) $true }
    & $aos capture screenshot window --app=mspaint "--out=$PSScriptRoot\shot-paint-window.png" 2>&1 | Out-Null
  }
  if ($paint) { Stop-Process -Id $paint.Id -Force -ErrorAction SilentlyContinue }
}

# Clean up our WT tabs (each closes its window when it is the last).
foreach ($t in $A1, $B1, $B2) { & $aos tab close --title=$t --app=WindowsTerminal | ForEach-Object { Write-Host "  $_" } }
Start-Sleep 2
Shot 'zz-after-cleanup'
Write-Host ("WT windows of ours left: " + @((& $aos window list --json | ConvertFrom-Json) | Where-Object { $_.Title -like "*$tag*" }).Count)
$results | Format-Table Step, Result, Landed, ForegroundKept -AutoSize | Out-String -Width 200 | Write-Host
$results | ConvertTo-Json | Set-Content (Join-Path $PSScriptRoot 'shot-wt-results.json')
