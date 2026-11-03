# Spike: can agent-launched apps live on a hidden secondary desktop, where no
# input aos sends can ever reach the owner's desktop? It creates desktop
# "aos-agent", starts a WinForms app there, runs aos.exe ON that desktop to
# list / type / click / read it, checks the owner's foreground never changes,
# then closes everything and the desktop.
#   cmd /c powershell -NoProfile -ExecutionPolicy Bypass -File hidden-desktop-spike.ps1
$ErrorActionPreference = 'Continue'
$aos = Join-Path $PSScriptRoot 'aos.exe'
Add-Type @'
using System; using System.Runtime.InteropServices; using System.Text;
public static class HD {
  [DllImport("user32.dll", CharSet=CharSet.Unicode, SetLastError=true)] public static extern IntPtr CreateDesktop(string name, IntPtr dev, IntPtr mode, int flags, uint access, IntPtr sa);
  [DllImport("user32.dll")] public static extern bool CloseDesktop(IntPtr h);
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr h, StringBuilder s, int n);
  [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)] public struct SI { public int cb; public string r; public string desk; public string title; public int x,y,xs,ys,xc,yc,fill,flags; public short show, r2; public IntPtr r3, hin, hout, herr; }
  [StructLayout(LayoutKind.Sequential)] public struct PI { public IntPtr hp, ht; public int pid, tid; }
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)] public static extern bool CreateProcess(string app, StringBuilder cmd, IntPtr pa, IntPtr ta, bool inherit, uint flags, IntPtr env, string cwd, ref SI si, out PI pi);
  [DllImport("kernel32.dll")] public static extern uint WaitForSingleObject(IntPtr h, uint ms);
  [DllImport("kernel32.dll")] public static extern bool GetExitCodeProcess(IntPtr h, out uint code);
  public static int Run(string desk, string cmd, uint waitMs, out uint exit) {
    var si = new SI(); si.cb = Marshal.SizeOf(si); si.desk = desk; PI pi;
    exit = 999;
    if (!CreateProcess(null, new StringBuilder(cmd), IntPtr.Zero, IntPtr.Zero, false, 0x08000000 /*CREATE_NO_WINDOW*/, IntPtr.Zero, null, ref si, out pi)) return -Marshal.GetLastWin32Error();
    if (waitMs > 0) { WaitForSingleObject(pi.hp, waitMs); GetExitCodeProcess(pi.hp, out exit); }
    return pi.pid;
  }
  public static string Fg() { var s = new StringBuilder(256); GetWindowText(GetForegroundWindow(), s, 256); return s.ToString(); }
}
'@
$desk = 'aos-agent'
$h = [HD]::CreateDesktop($desk, [IntPtr]::Zero, [IntPtr]::Zero, 0, 0x10000000 -bor 0x01FF, [IntPtr]::Zero)  # GENERIC_ALL | DESKTOP_ALL
if ($h -eq [IntPtr]::Zero) { Write-Host "FAIL CreateDesktop"; exit 1 }
Write-Host "created desktop WinSta0\$desk"
$fg0 = [HD]::Fg()
$out = Join-Path $PSScriptRoot 'hd-out.txt'
$form = "Add-Type -AssemblyName System.Windows.Forms; `$f = New-Object Windows.Forms.Form; `$f.Text = 'hidden-form'; `$t = New-Object Windows.Forms.TextBox; `$t.Multiline = `$true; `$t.Dock = 'Fill'; `$t.Text = 'start'; `$b = New-Object Windows.Forms.Button; `$b.Text = 'ClickMe'; `$b.Dock = 'Bottom'; `$b.Add_Click({ `$t.AppendText(' clicked') }); `$f.Controls.Add(`$t); `$f.Controls.Add(`$b); [void]`$f.ShowDialog()"
$enc = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($form))
$x = 0
$formPid = [HD]::Run("WinSta0\$desk", "powershell.exe -NoProfile -EncodedCommand $enc", 0, [ref]$x)
Write-Host "form pid on hidden desktop: $formPid"
Start-Sleep 4
function OnDesk($args1) {
  $code = 0
  $cmd = "cmd.exe /c `"`"$aos`" $args1 >> `"$out`" 2>&1`""
  [void][HD]::Run("WinSta0\$desk", $cmd, 20000, [ref]$code)
  return $code
}
Set-Content $out ''
Add-Content $out '--- window list (run on the hidden desktop)'
OnDesk 'window list' | Out-Null
Add-Content $out '--- key type'
OnDesk 'key type --title=hidden-form " typed-on-hidden-desktop" --json' | Out-Null
Add-Content $out '--- mouse click --find'
OnDesk 'mouse click --title=hidden-form --find=ClickMe' | Out-Null
Add-Content $out '--- window text'
OnDesk 'window text --title=hidden-form' | Out-Null
Add-Content $out '--- screenshot of the hidden desktop'
OnDesk "capture screenshot fullscreen --out=$PSScriptRoot\hd-shot.png" | Out-Null
$fg1 = [HD]::Fg()
Get-Content $out | ForEach-Object { Write-Host "  $_" }
Write-Host "owner foreground before: '$fg0' after: '$fg1' -> $(if ($fg0 -eq $fg1) {'UNCHANGED'} else {'CHANGED'})"
Write-Host ("owner desktop window list has hidden-form: " + [bool]((& $aos window list) -match 'hidden-form'))
if ($formPid -gt 0) { Stop-Process -Id $formPid -Force -ErrorAction SilentlyContinue }
Start-Sleep 1
Write-Host "CloseDesktop: $([HD]::CloseDesktop($h))"
