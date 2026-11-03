# Focus-free input check for aos on Windows. Self-contained: put aos.exe next
# to this script (or on PATH) and run it in a desktop session:
#   powershell -ExecutionPolicy Bypass -File focusfree.ps1
# It opens Explorer (window A, kept in front), Notepad and Calculator, drives
# Notepad and Calculator through aos, and after every step checks that the
# foreground window did not change and that the effect landed. It closes
# only what it opened, and skips Notepad / Calculator if they were already
# running (so it never touches a window you are using).
$ErrorActionPreference = 'Continue'
$aos = Join-Path $PSScriptRoot 'aos.exe'
if (-not (Test-Path $aos)) { $aos = 'aos' }
$env:AOS_NO_SKILL_AUTOINSTALL = '1'
Add-Type @'
using System; using System.Runtime.InteropServices; using System.Text;
public static class AosFg {
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr h, StringBuilder s, int n);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool GetCursorPos(out POINT p);
  public struct POINT { public int X; public int Y; }
  public static string Title(IntPtr h) { var s = new StringBuilder(512); GetWindowText(h, s, 512); return s.ToString(); }
}
'@
$results = @()
function Fg { $h = [AosFg]::GetForegroundWindow(); "{0} '{1}'" -f $h, [AosFg]::Title($h) }
function Cursor { $p = New-Object AosFg+POINT; [void][AosFg]::GetCursorPos([ref]$p); "$($p.X),$($p.Y)" }
function Check($name, [scriptblock]$act, [scriptblock]$landed) {
  $fg0 = Fg; $c0 = Cursor
  $out = & $act 2>&1 | Out-String
  $code = $LASTEXITCODE
  Start-Sleep -Milliseconds 400
  $fg1 = Fg; $c1 = Cursor
  $ok = & $landed $out
  $kept = ($fg0 -eq $fg1)
  $status = if ($ok -and $kept -and ($c0 -eq $c1)) { 'PASS' } else { 'FAIL' }
  $script:results += [pscustomobject]@{ Test = $name; Result = $status; Landed = [bool]$ok; ForegroundKept = $kept; PointerKept = ($c0 -eq $c1); Exit = $code }
  Write-Host ("[{0}] {1}  exit={2} landed={3} fgKept={4} ptrKept={5}" -f $status, $name, $code, [bool]$ok, $kept, ($c0 -eq $c1))
  Write-Host ("    out: " + ($out.Trim() -replace "`r?`n", ' | ').Substring(0, [Math]::Min(300, $out.Trim().Length)))
  if (-not $kept) { Write-Host "    foreground: $fg0 -> $fg1" }
}

$tag = 'aoswt' + (Get-Random -Maximum 99999)
$dir = Join-Path $env:TEMP $tag
New-Item -ItemType Directory -Force $dir | Out-Null
$file = Join-Path $dir "$tag.txt"
Set-Content $file 'start'

# Window A: an Explorer window on our temp folder, brought to the front.
Start-Process explorer.exe $dir
Start-Sleep 2
$shell = New-Object -ComObject Shell.Application
$exp = $shell.Windows() | Where-Object { $_.LocationURL -like "*$tag*" } | Select-Object -First 1
if ($exp) { [void][AosFg]::SetForegroundWindow([IntPtr]$exp.HWND) }

$hadNotepad = @(Get-Process notepad -ErrorAction SilentlyContinue).Count -gt 0
$hadCalc = @(Get-Process CalculatorApp -ErrorAction SilentlyContinue).Count -gt 0
$np = $null
if (-not $hadNotepad) {
  $np = Start-Process notepad.exe $file -PassThru
  Start-Sleep 2
} else { Write-Host 'SKIP notepad: Notepad was already running' }
if (-not $hadCalc) { Start-Process calc.exe; Start-Sleep 3 } else { Write-Host 'SKIP calculator: it was already running' }
# A classic Win32 edit control we own: a WinForms TextBox in its own process.
$formTitle = "aosform-$tag"
$formScript = "Add-Type -AssemblyName System.Windows.Forms; `$f = New-Object Windows.Forms.Form; `$f.Text = '$formTitle'; `$f.Width = 500; `$f.Height = 300; `$t = New-Object Windows.Forms.TextBox; `$t.Multiline = `$true; `$t.Dock = 'Fill'; `$t.Text = 'start'; `$f.Controls.Add(`$t); [void]`$f.ShowDialog()"
$form = Start-Process powershell.exe -ArgumentList '-NoProfile', '-Command', $formScript -PassThru
Start-Sleep 3
if ($exp) { [void][AosFg]::SetForegroundWindow([IntPtr]$exp.HWND); Start-Sleep -Milliseconds 500 }
Write-Host "window A (foreground): $(Fg)"
# Baseline: nothing run. Apps that activate late after launch show up here, not as aos.
Check 'baseline: no aos call (late app activation)' { 'idle' } { param($o) $true }
if ($exp) { [void][AosFg]::SetForegroundWindow([IntPtr]$exp.HWND); Start-Sleep -Milliseconds 500 }
Check 'winforms edit: key type --title (EM_REPLACESEL)' { & $aos key type --title=$formTitle ' typed by aos' --json } { param($o) (& $aos window text --title=$formTitle --source=accessibility | Out-String) -match 'typed by aos' }
Check 'winforms edit: key press enter --title (posted)' { & $aos key press enter --title=$formTitle --json } { param($o) $o -match 'post' }
Check 'winforms edit: window text' { & $aos window text --title=$formTitle --json } { param($o) $o -match '"source": "uia"' -and $o -match 'typed by aos' }
& $aos window list | Select-String -Pattern $tag, 'Calculator' | ForEach-Object { Write-Host "    $_" }

if (-not $hadNotepad) {
  Check 'notepad: key type --title' { & $aos key type --title=$tag 'hello from aos' --json } { param($o) (& $aos window text --title=$tag --source=accessibility | Out-String) -match 'hello from aos' }
  Check 'notepad: key press enter --title' { & $aos key press enter --title=$tag --json } { param($o) $LASTEXITCODE -eq 0 -or $o -match 'post' }
  Check 'notepad: window text' { & $aos window text --title=$tag --json } { param($o) $o -match '"source": "uia"' -and $o -match 'typed by aos' }
  Check 'notepad: find --text=start' { & $aos find --title=$tag --text=start } { param($o) $o -match 'start' }
}
if (-not $hadCalc) {
  Check 'calc: find --text=Seven' { & $aos find --title=Calculator --text=Seven --role=button } { param($o) $o -match 'Seven' }
  Check 'calc: mouse click --find=Seven' { & $aos mouse click --title=Calculator --find=Seven } { param($o) $o -match 'press' }
  Check 'calc: mouse click --find=Plus' { & $aos mouse click --title=Calculator --find=Plus } { param($o) $o -match 'press' }
  Check 'calc: mouse click --find=Two' { & $aos mouse click --title=Calculator --find=Two } { param($o) $o -match 'press' }
  Check 'calc: mouse click --find=Equals' { & $aos mouse click --title=Calculator --find=Equals } { param($o) (& $aos window text --title=Calculator --source=accessibility | Out-String) -match 'Display is 9' }
  Check 'calc (WinUI): key type --title (expect refusal, never a focus steal)' { & $aos key type --title=Calculator '5' } { param($o) $o -match 'focus' -or $o -match 'SetValue' -or $o -match 'editable' }
}

# Polite guard: a pointer move to where the pointer already is. Exit 0 when
# the person is idle, exit 5 (refused) when they used the machine in the
# last 5 s; either way nothing moves.
Check 'polite: mouse move to own position (exit 0 idle / 5 active)' { $p = ((& $aos mouse position) -split '[^0-9-]+' | Where-Object { $_ -ne '' }); & $aos mouse move $p[0] $p[1] } { param($o) $LASTEXITCODE -eq 0 -or $LASTEXITCODE -eq 5 }
& $aos obs focus --limit=3 | ForEach-Object { Write-Host "    obs: $_" }

# Clean up only what we opened.
if ($form) { Stop-Process -Id $form.Id -Force -ErrorAction SilentlyContinue }
if ($np) { Stop-Process -Id $np.Id -Force -ErrorAction SilentlyContinue }
if (-not $hadCalc) { Get-Process CalculatorApp -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue }
if ($exp) { $exp.Quit() }
Remove-Item -Recurse -Force $dir -ErrorAction SilentlyContinue

$results | Format-Table -AutoSize | Out-String | Write-Host
$results | ConvertTo-Json | Set-Content (Join-Path $PSScriptRoot 'focusfree-results.json')
if ($results | Where-Object Result -eq 'FAIL') { exit 1 } else { exit 0 }
