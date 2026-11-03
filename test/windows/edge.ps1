# Edge, focus-free: a local test page in a new InPrivate window behind an
# existing window (Notepad if open), driven with aos tab list / read / type /
# click and window text. Checks the foreground after every step, saves
# before/after screenshots, closes only the InPrivate window it opened.
#   cmd /c powershell -NoProfile -ExecutionPolicy Bypass -File edge.ps1
$ErrorActionPreference = 'Continue'
$aos = Join-Path $PSScriptRoot 'aos.exe'
$env:AOS_NO_SKILL_AUTOINSTALL = '1'; $env:AOS_CALLER = 'edge.ps1'
Add-Type @'
using System; using System.Runtime.InteropServices; using System.Text;
public static class W {
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr h, StringBuilder s, int n);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h, uint m, IntPtr w, IntPtr l);
  public static string Fg() { var s = new StringBuilder(256); GetWindowText(GetForegroundWindow(), s, 256); return GetForegroundWindow() + " '" + s + "'"; }
}
'@
$results = @(); $n = 0
function Shot($name) { $p = Join-Path $PSScriptRoot "shot-$name.png"; & $aos capture screenshot fullscreen "--out=$p" 2>&1 | Out-Null }
function Check($what, [scriptblock]$act, [scriptblock]$landed) {
  $script:n++; $id = '{0:d2}-edge-{1}' -f $script:n, (($what -replace '[^A-Za-z0-9]+', '-').Trim('-').ToLower())
  Start-Sleep -Milliseconds 800  # let an earlier hand-back settle
  Shot "$id-before"; $fg0 = [W]::Fg()
  $out = & $act 2>&1 | Out-String
  Start-Sleep -Milliseconds 1500
  $fg1 = [W]::Fg(); Shot "$id-after"
  $ok = [bool](& $landed $out); $kept = $fg0 -eq $fg1
  $r = $(if ($ok -and $kept) { 'PASS' } else { 'FAIL' })
  $script:results += [pscustomobject]@{ Step = $what; Result = $r; Landed = $ok; ForegroundKept = $kept; Shots = $id }
  Write-Host ("[{0}] {1}  landed={2} fgKept={3}" -f $r, $what, $ok, $kept)
  $o = ($out.Trim() -replace "`r?`n", ' | '); Write-Host ("    " + $o.Substring(0, [Math]::Min(500, $o.Length)))
  if (-not $kept) { Write-Host "    foreground: $fg0 -> $fg1" }
}
$tag = 'edge' + (Get-Random -Maximum 9999)
$dir = Join-Path $env:TEMP $tag; New-Item -ItemType Directory -Force $dir | Out-Null
$page = Join-Path $dir "$tag.html"
@"
<!doctype html><html><head><title>$tag test page</title></head><body>
<h1>aos edge test</h1>
<label>Message <input id="msg" aria-label="Message" placeholder="Message"></label>
<button id="send" onclick="document.getElementById('out').textContent='sent: '+document.getElementById('msg').value">Send</button>
<p id="out">nothing sent</p>
<form onsubmit="document.getElementById('sub').textContent='submitted: '+document.getElementById('q').value; return false"><input id="q" aria-label="Search" placeholder="Search"></form>
<p id="sub">not submitted</p>
</body></html>
"@ | Set-Content -Encoding UTF8 $page
$before = @((& $aos window list --json | ConvertFrom-Json) | Where-Object { $_.App -eq 'msedge' } | ForEach-Object { $_.ID })
Start-Process msedge.exe -ArgumentList '--inprivate', '--new-window', ([Uri]$page).AbsoluteUri
Start-Sleep 6
$mine = (& $aos window list --json | ConvertFrom-Json) | Where-Object { $_.App -eq 'msedge' -and $_.Title -like "*$tag*" } | Select-Object -First 1
Write-Host "our edge window: $($mine.ID) '$($mine.Title)'  (edge windows before: $($before.Count))"
$front = (& $aos window list --json | ConvertFrom-Json) | Where-Object { $_.App -eq 'Notepad' } | Select-Object -First 1
if (-not $front) { $front = (& $aos window list --json | ConvertFrom-Json) | Where-Object { $_.App -ne 'msedge' } | Select-Object -First 1 }
[void][W]::SetForegroundWindow([IntPtr][int64]$front.ID); Start-Sleep 1
Write-Host "front: $([W]::Fg())"

# The page's accessibility tree updates a moment after an action: read with retries.
function PageHas($pat) { for ($i = 0; $i -lt 6; $i++) { if ((& $aos tab read --title=$tag | Out-String) -match $pat) { return $true }; Start-Sleep -Milliseconds 500 }; $false }
Check 'tab list finds the page' { & $aos tab list --app=msedge --json } { param($o) $o -match $tag }
Check 'tab read (page as markdown with refs)' { & $aos tab read --title=$tag } { param($o) $o -match 'aos edge test' -and $o -match 'Send' }
Check 'tab type into Message field (Notepad in front)' { & $aos tab type --title=$tag --into=Message --text='hello-edge' } { param($o) $o -match 'SetValue' -and (PageHas 'hello-edge') }
Check 'tab click Send (Notepad in front)' { & $aos tab click --title=$tag --find=Send --role=button } { param($o) PageHas 'sent: hello-edge' }
Check 'window text of the edge window' { & $aos window text --id=$($mine.ID) } { param($o) $o -match 'sent: hello-edge' }
# A terminal in front instead of Notepad.
& $aos tab open --app=WindowsTerminal --wt-window="aosE-$tag" --title="E-$tag" | Out-Null; Start-Sleep 3
$term = (& $aos window list --json | ConvertFrom-Json) | Where-Object { $_.Title -eq "E-$tag" } | Select-Object -First 1
if ($term) { [void][W]::SetForegroundWindow([IntPtr][int64]$term.ID); Start-Sleep 1 }
Write-Host "front: $([W]::Fg())"
Check 'tab type (terminal in front)' { & $aos tab type --title=$tag --into=Message --text='typed-while-terminal-in-front' } { param($o) $o -match 'SetValue' }
Check 'tab click Send (terminal in front)' { & $aos tab click --title=$tag --find=Send --role=button } { param($o) (PageHas 'sent: typed-while-terminal-in-front') -and (PageHas 'submitted: query-aos') }
Check 'tab type --enter submits (foreground taken for Enter, handed back)' { & $aos tab type --title=$tag --into=Search --text='query-aos' --enter --force } { param($o) $o -match 'typed into' }
if ($term) { & $aos tab close --title="E-$tag" --app=WindowsTerminal | Out-Null }
Start-Sleep 2
# Chromium does not update a fully covered window's accessibility tree, so
# what was typed while the terminal covered it is checked once it is visible.
Check 'after the terminal closed: the typing and the click had landed' { 'check' } { param($o) $o -match 'clicked' }

# Close only our InPrivate window.
if ($mine) { [void][W]::PostMessage([IntPtr][int64]$mine.ID, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero) }
Start-Sleep 2
Remove-Item -Recurse -Force $dir -ErrorAction SilentlyContinue
Shot 'zz-edge-after-cleanup'
Write-Host ("our edge window left: " + @((& $aos window list --json | ConvertFrom-Json) | Where-Object { $_.Title -like "*$tag*" }).Count + "; edge windows now: " + @((& $aos window list --json | ConvertFrom-Json) | Where-Object { $_.App -eq 'msedge' }).Count)
$results | Format-Table Step, Result, Landed, ForegroundKept -AutoSize | Out-String -Width 200 | Write-Host
$results | ConvertTo-Json | Set-Content (Join-Path $PSScriptRoot 'shot-edge-results.json')
