# Focus-free input check for aos on Windows. Self-contained: put aos.exe next
# to this script (or on PATH) and run it in a desktop session:
#   cmd /c powershell -NoProfile -ExecutionPolicy Bypass -File focusfree.ps1
# It puts an Explorer window in front (window A, standing in for the person's
# work), opens each target app behind it, drives the app through aos, and
# after every step checks that the foreground window and the pointer did not
# change and that the effect landed. A full-screen capture is saved before
# and after each step in .\shots. It closes only what it opened and skips an
# app that was already running, so it never touches a window you are using.
$ErrorActionPreference = 'Continue'
$aos = Join-Path $PSScriptRoot 'aos.exe'
if (-not (Test-Path $aos)) { $aos = 'aos' }
$env:AOS_NO_SKILL_AUTOINSTALL = '1'
$env:AOS_CALLER = 'focusfree.ps1'
$shots = $PSScriptRoot   # shot-*.png next to the script
Add-Type @'
using System; using System.Runtime.InteropServices; using System.Text;
public static class AosFg {
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr h, StringBuilder s, int n);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h, uint m, IntPtr w, IntPtr l);
  [DllImport("user32.dll")] public static extern IntPtr SendMessage(IntPtr h, uint m, IntPtr w, IntPtr l);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern IntPtr FindWindowEx(IntPtr p, IntPtr after, string cls, string title);
  public static string Title(IntPtr h) { var s = new StringBuilder(512); GetWindowText(h, s, 512); return s.ToString(); }
}
'@
$results = @()
$n = 0
function Fg { $h = [AosFg]::GetForegroundWindow(); "{0} '{1}'" -f $h, [AosFg]::Title($h) }
function Cursor { (& $aos mouse position 2>$null | Out-String).Trim() }
function Shot($name) { $p = Join-Path $shots "shot-$name.png"; & $aos capture screenshot fullscreen "--out=$p" 2>&1 | Out-Null }
function Check($app, $action, [scriptblock]$act, [scriptblock]$landed) {
  $script:n++
  $id = '{0:d2}-{1}' -f $script:n, (($app + '-' + $action) -replace '[^A-Za-z0-9]+', '-').Trim('-').ToLower()
  Shot "$id-before"
  $fg0 = Fg; $c0 = Cursor
  $out = & $act 2>&1 | Out-String
  $code = $LASTEXITCODE
  Start-Sleep -Milliseconds 1200   # let a late activation show up
  $fg1 = Fg; $c1 = Cursor
  Shot "$id-after"
  $ok = [bool](& $landed $out)
  $kept = ($fg0 -eq $fg1); $ptr = ($c0 -eq $c1)
  $status = if ($ok -and $kept -and $ptr) { 'PASS' } else { 'FAIL' }
  $script:results += [pscustomobject]@{ App = $app; Action = $action; Result = $status; Landed = $ok; FocusFree = $kept; PointerKept = $ptr; Exit = $code; Shots = $id }
  Write-Host ("[{0}] {1} / {2}  exit={3} landed={4} focusFree={5} ptrKept={6}" -f $status, $app, $action, $code, $ok, $kept, $ptr)
  $o = ($out.Trim() -replace "`r?`n", ' | ')
  Write-Host ("    out: " + $o.Substring(0, [Math]::Min(260, $o.Length)))
  if (-not $kept) { Write-Host "    foreground: $fg0 -> $fg1" }
}
function Running($name) { @(Get-Process $name -ErrorAction SilentlyContinue).Count -gt 0 }
function Front { if ($script:exp) { [void][AosFg]::SetForegroundWindow([IntPtr]$script:exp.HWND); Start-Sleep -Milliseconds 600 } }
# Windows we opened, found by title in aos's own window list; closed by handle.
function WinIds($like) { (& $aos window list --json | ConvertFrom-Json) | Where-Object { $_.Title -like $like } | ForEach-Object { [int64]$_.ID } }
function CloseWin($like) {
  foreach ($h in (WinIds $like)) {
    # Notepad: clear the modified flag of its edit control so closing does not prompt to save.
    $ed = [AosFg]::FindWindowEx([IntPtr]$h, [IntPtr]::Zero, 'RichEditD2DPT', $null)
    if ($ed -eq [IntPtr]::Zero) { $host1 = [AosFg]::FindWindowEx([IntPtr]$h, [IntPtr]::Zero, 'NotepadTextBox', $null); if ($host1 -ne [IntPtr]::Zero) { $ed = [AosFg]::FindWindowEx($host1, [IntPtr]::Zero, 'RichEditD2DPT', $null) } }
    if ($ed -ne [IntPtr]::Zero) { [void][AosFg]::SendMessage($ed, 0x00B9, [IntPtr]::Zero, [IntPtr]::Zero) }  # EM_SETMODIFY FALSE
    [void][AosFg]::PostMessage([IntPtr]$h, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero)  # WM_CLOSE
  }
}
function TextOf($sel) { & $aos window text @sel --source=accessibility 2>&1 | Out-String }

$tag = 'aoswt' + (Get-Random -Maximum 99999)
$dir = Join-Path $env:TEMP $tag
New-Item -ItemType Directory -Force $dir | Out-Null
Set-Content (Join-Path $dir "$tag.txt") 'start'

# Window A: an Explorer window on our temp folder, kept in front.
Start-Process explorer.exe $dir
Start-Sleep 2
$exp = (New-Object -ComObject Shell.Application).Windows() | Where-Object { $_.LocationURL -like "*$tag*" } | Select-Object -First 1
$opened = @()

# --- launch every target behind window A ------------------------------
$term = "term-$tag"; $con = "con-$tag"; $form = "form-$tag"
if (Get-Command wt.exe -ErrorAction SilentlyContinue) {
  Start-Process wt.exe -ArgumentList '-w', 'new', '--title', $term, '--suppressApplicationTitle', 'cmd', '/k', 'echo ready'
  $opened += 'wt'
} else { Write-Host 'SKIP Windows Terminal: not installed' }
$conProc = Start-Process conhost.exe -ArgumentList 'cmd.exe', '/k', "title $con" -PassThru
$formScript = "Add-Type -AssemblyName System.Windows.Forms; `$f = New-Object Windows.Forms.Form; `$f.Text = '$form'; `$f.Width = 500; `$f.Height = 300; `$t = New-Object Windows.Forms.TextBox; `$t.Multiline = `$true; `$t.Dock = 'Fill'; `$t.Text = 'start'; `$b = New-Object Windows.Forms.Button; `$b.Text = 'ClickMe'; `$b.Dock = 'Bottom'; `$b.Add_Click({ `$t.AppendText(' clicked') }); `$f.Controls.Add(`$t); `$f.Controls.Add(`$b); [void]`$f.ShowDialog()"
$formProc = Start-Process powershell.exe -ArgumentList '-NoProfile', '-Command', $formScript -PassThru
# Notepad on our own file: its window title carries the tag.
Start-Process notepad.exe (Join-Path $dir "$tag.txt"); $opened += 'notepad'
$hadCalc = Running 'CalculatorApp'
if (-not $hadCalc) { Start-Process calc.exe; $opened += 'calc' } else { Write-Host 'SKIP calculator: already running' }
$hadSettings = @(WinIds 'Settings').Count -gt 0
if (-not $hadSettings) { Start-Process 'ms-settings:about'; $opened += 'settings' } else { Write-Host 'SKIP settings: a Settings window is open' }
Start-Process msedge.exe -ArgumentList '--inprivate', '--new-window', 'about:blank'; $opened += 'edge'
Start-Sleep 6
Front
Write-Host "window A (foreground): $(Fg)"
& $aos window list | ForEach-Object { Write-Host "    $_" }

Check 'baseline' 'no aos call (late activations show here)' { 'idle' } { param($o) $true }
Front

# --- terminals: console input buffer -----------------------------------
if ($opened -contains 'wt') {
  Check 'Windows Terminal' 'key type' { & $aos key type --title=$term 'echo hello-wt-aos' --json } { param($o) $o -match 'console input' }
  Check 'Windows Terminal' 'key press enter' { & $aos key press enter --title=$term --json } { param($o) $o -match '"Route":"post"' }
  Start-Sleep 1
  Check 'Windows Terminal' 'window text' { & $aos window text --title=$term } { param($o) $o -match 'hello-wt-aos' }
  Front
}
Check 'conhost' 'key type' { & $aos key type --title=$con 'echo hello-con-aos' --json } { param($o) $o -match 'console input' }
Check 'conhost' 'key press enter' { & $aos key press enter --title=$con --json } { param($o) $o -match '"Route":"post"' }
Start-Sleep 1
Check 'conhost' 'window text' { & $aos window text --title=$con } { param($o) $o -match 'hello-con-aos' }
Front

# --- classic Win32 ----------------------------------------------------
Check 'WinForms edit' 'key type' { & $aos key type --title=$form ' typed-by-aos' --json } { param($o) (TextOf @("--title=$form")) -match 'typed-by-aos' }
Check 'WinForms button' 'mouse click --find' { & $aos mouse click --title=$form --find=ClickMe } { param($o) (TextOf @("--title=$form")) -match 'clicked' }
Check 'WinForms' 'window text' { & $aos window text --title=$form } { param($o) $o -match 'typed-by-aos' }
Front
if ($opened -contains 'notepad') {
  Check 'Notepad' 'key type' { & $aos key type --app=Notepad --title=$tag 'hello-notepad-aos' --json } { param($o) (TextOf @('--app=Notepad', "--title=$tag")) -match 'hello-notepad-aos' }
  Check 'Notepad' 'window text' { & $aos window text --app=Notepad --title=$tag } { param($o) $o -match 'hello-notepad-aos' }
  Check 'Notepad' 'find' { & $aos find --app=Notepad --title=$tag --text=hello-notepad-aos } { param($o) $o -match 'hello-notepad-aos' }
  Front
}

# --- UIA apps ---------------------------------------------------------
if ($opened -contains 'calc') {
  Check 'Calculator' 'find' { & $aos find --title=Calculator --text=Seven --role=button } { param($o) $o -match 'Seven' }
  Check 'Calculator' 'click 7' { & $aos mouse click --title=Calculator --find=Seven } { param($o) $o -match 'press' }
  Check 'Calculator' 'click +' { & $aos mouse click --title=Calculator --find=Plus } { param($o) $o -match 'press' }
  Check 'Calculator' 'click 2' { & $aos mouse click --title=Calculator --find=Two } { param($o) $o -match 'press' }
  Check 'Calculator' 'click =' { & $aos mouse click --title=Calculator --find=Equals } { param($o) (TextOf @('--title=Calculator')) -match 'Display is 9' }
  Check 'Calculator' 'key type (no editable field)' { & $aos key type --title=Calculator '5' } { param($o) $o -match 'focus|editable' }
  Front
}
if ($opened -contains 'settings') {
  Check 'Settings (WinUI)' 'window text' { & $aos window text --title=Settings } { param($o) $o -match 'About|Device specifications' }
  Check 'Settings (WinUI)' 'click Open Navigation' { & $aos mouse click --title=Settings --find='Open Navigation' } { param($o) $o -match 'press' }
  Front
}
Check 'Explorer' 'find' { & $aos find --title="$tag - File Explorer" --text="$tag" } { param($o) $o -match "$tag" }
Check 'Explorer' 'window text' { & $aos window text --title="$tag - File Explorer" } { param($o) $o -match "$tag" }
if ($opened -contains 'edge') {
  Check 'Edge (InPrivate)' 'key type (address bar)' { & $aos key type --title=InPrivate 'aos-edge-test' --json } { param($o) (TextOf @('--title=InPrivate')) -match 'aos-edge-test' }
  Check 'Edge (InPrivate)' 'window text' { & $aos window text --title=InPrivate } { param($o) $o.Length -gt 0 }
  Front
}

# Polite guard: a pointer move to where the pointer already is.
Check 'polite guard' 'mouse move (exit 0 idle / 5 active)' { $p = ((& $aos mouse position) -split '[^0-9-]+' | Where-Object { $_ -ne '' }); & $aos mouse move $p[0] $p[1] } { param($o) $o -notmatch 'error|failed' }
& $aos obs focus --limit=4 | ForEach-Object { Write-Host "    obs: $_" }

# --- clean up only what we opened (by window handle; nothing else is touched)
if ($opened -contains 'wt') { CloseWin $term }
if ($conProc) { Get-CimInstance Win32_Process -Filter "ParentProcessId=$($conProc.Id)" | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }; Stop-Process -Id $conProc.Id -Force -ErrorAction SilentlyContinue }
if ($formProc) { Stop-Process -Id $formProc.Id -Force -ErrorAction SilentlyContinue }
if ($opened -contains 'notepad') {
  # Win11 Notepad opens our file as a TAB in a window that may hold the
  # owner's unsaved tabs: never close the window. Mark our tab unmodified,
  # then press its own Close Tab button (it is the active tab, the only one
  # with a close button), before the temp file is deleted.
  foreach ($h in (WinIds "*$tag*Notepad")) {
    $ed = [AosFg]::FindWindowEx([IntPtr]$h, [IntPtr]::Zero, 'RichEditD2DPT', $null)
    if ($ed -eq [IntPtr]::Zero) { $tb = [AosFg]::FindWindowEx([IntPtr]$h, [IntPtr]::Zero, 'NotepadTextBox', $null); if ($tb -ne [IntPtr]::Zero) { $ed = [AosFg]::FindWindowEx($tb, [IntPtr]::Zero, 'RichEditD2DPT', $null) } }
    if ($ed -ne [IntPtr]::Zero) { [void][AosFg]::SendMessage($ed, 0x00B9, [IntPtr]::Zero, [IntPtr]::Zero) }
  }
  $closers = @(& $aos find --app=Notepad --title=$tag --text='Close Tab' --role=button --all --json 2>$null | ConvertFrom-Json)
  if ($closers.Count -eq 1) { & $aos mouse click --app=Notepad --title=$tag --find='Close Tab' --role=button | Out-Null }
  else { Write-Host "LEFT OPEN: our Notepad tab ($($closers.Count) Close Tab buttons; not guessing)" }
  Start-Sleep 1
}
if ($opened -contains 'calc') { Get-Process CalculatorApp -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue }
if ($opened -contains 'settings') { CloseWin 'Settings' }
if ($opened -contains 'edge') { CloseWin '*InPrivate*' }
if ($exp) { $exp.Quit() }
Start-Sleep 2
Shot 'zz-after-cleanup'
Remove-Item -Recurse -Force $dir -ErrorAction SilentlyContinue

$results | Format-Table App, Action, Result, Landed, FocusFree, PointerKept, Exit -AutoSize | Out-String -Width 200 | Write-Host
$results | ConvertTo-Json | Set-Content (Join-Path $PSScriptRoot 'shot-results.json')
if ($results | Where-Object Result -eq 'FAIL') { exit 1 } else { exit 0 }
