package groups

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/muthuishere/aos/internal/sys"
)

// sayNativeExt is what both Windows engines write without conversion.
const sayNativeExt = ".wav"

// sayPS is shared by render and voices. WinRT (Windows.Media.SpeechSynthesis)
// comes first: it is where the Natural / OneCore voices live. System.Speech
// (SAPI 5) is the fallback for machines whose PowerShell cannot reach WinRT.
// Inputs come through AOS_SAY_* environment variables, never the command line,
// so no text needs PowerShell quoting.
const sayPS = `
$ErrorActionPreference = 'Stop'
$mode = $env:AOS_SAY_MODE
$text = $env:AOS_SAY_TEXT
$want = $env:AOS_SAY_VOICE
$wpm = [int]$env:AOS_SAY_RATE
$out = $env:AOS_SAY_OUT
$list = @()
$winrtErr = $null
try {
  Add-Type -AssemblyName System.Runtime.WindowsRuntime
  $null = [Windows.Media.SpeechSynthesis.SpeechSynthesizer, Windows.Media.SpeechSynthesis, ContentType = WindowsRuntime]
  $all = [Windows.Media.SpeechSynthesis.SpeechSynthesizer]::AllVoices
  foreach ($v in $all) { $list += [pscustomobject]@{ Name = $v.DisplayName; Language = $v.Language; Gender = [string]$v.Gender; Engine = 'winrt' } }
  if ($mode -eq 'render') {
    $s = New-Object Windows.Media.SpeechSynthesis.SpeechSynthesizer
    if ($want) {
      $v = $all | Where-Object { $_.DisplayName -eq $want -or $_.DisplayName -like "*$want*" } | Select-Object -First 1
      if (-not $v) { throw "NOVOICE" }
      $s.Voice = $v
    }
    if ($wpm -gt 0) { $s.Options.SpeakingRate = [Math]::Max(0.5, [Math]::Min(6.0, $wpm / 175.0)) }
    $asTask = [System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object { $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation` + "`" + `1' } | Select-Object -First 1
    $task = $asTask.MakeGenericMethod([Windows.Media.SpeechSynthesis.SpeechSynthesisStream]).Invoke($null, @($s.SynthesizeTextToStreamAsync($text)))
    $stream = $task.Result
    $src = [System.IO.WindowsRuntimeStreamExtensions]::AsStreamForRead($stream)
    $dst = [System.IO.File]::Create($out)
    $src.CopyTo($dst); $dst.Close(); $src.Close()
    Write-Output 'ENGINE winrt'
    exit 0
  }
} catch {
  if ($_.Exception.Message -match 'NOVOICE') { $winrtErr = 'NOVOICE' } else { $winrtErr = $_.Exception.Message }
}
Add-Type -AssemblyName System.Speech
$sp = New-Object System.Speech.Synthesis.SpeechSynthesizer
foreach ($v in $sp.GetInstalledVoices()) {
  $i = $v.VoiceInfo
  if (-not ($list | Where-Object { $_.Name -eq $i.Name })) { $list += [pscustomobject]@{ Name = $i.Name; Language = $i.Culture.Name; Gender = [string]$i.Gender; Engine = 'sapi' } }
}
if ($mode -eq 'voices') { ConvertTo-Json -Compress -InputObject @($list); exit 0 }
if ($want) {
  $v = $sp.GetInstalledVoices() | Where-Object { $_.VoiceInfo.Name -eq $want -or $_.VoiceInfo.Name -like "*$want*" } | Select-Object -First 1
  if (-not $v) { [Console]::Error.WriteLine("no voice named '$want'"); exit 3 }
  $sp.SelectVoice($v.VoiceInfo.Name)
}
if ($wpm -gt 0) { $sp.Rate = [Math]::Max(-10, [Math]::Min(10, [int][Math]::Round(($wpm - 175) / 25.0))) }
$sp.SetOutputToWaveFile($out)
$sp.Speak($text)
$sp.Dispose()
Write-Output 'ENGINE sapi'
`

func encodePS(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, len(u)*2)
	for i, r := range u {
		b[2*i], b[2*i+1] = byte(r), byte(r>>8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func runSayPS(env ...string) (string, error) {
	exe := sys.FirstAvailable("powershell", "pwsh")
	if exe == "" {
		return "", errUnsupported
	}
	cmd := exec.Command(exe, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encodePS(sayPS))
	cmd.Env = append(os.Environ(), env...)
	hideChild(cmd)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("speech: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

func sayRender(req sayRequest, out string) error {
	_, err := runSayPS("AOS_SAY_MODE=render", "AOS_SAY_TEXT="+req.Text, "AOS_SAY_VOICE="+req.Voice,
		"AOS_SAY_RATE="+strconv.Itoa(req.Rate), "AOS_SAY_OUT="+out)
	if err != nil && strings.Contains(err.Error(), "no voice named") {
		return fmt.Errorf("%v (list voices with `aos say voices`)", err)
	}
	return err
}

// playCommand plays a wav synchronously with no window.
func playCommand(clip string) *exec.Cmd {
	exe := sys.FirstAvailable("powershell", "pwsh")
	script := "(New-Object System.Media.SoundPlayer $env:AOS_SAY_CLIP).PlaySync()"
	cmd := exec.Command(exe, "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "AOS_SAY_CLIP="+clip)
	return cmd
}

func sayVoices() ([]sayVoice, error) {
	out, err := runSayPS("AOS_SAY_MODE=voices")
	if err != nil {
		return nil, err
	}
	var voices []sayVoice
	if err := json.Unmarshal([]byte(out), &voices); err != nil {
		return nil, fmt.Errorf("reading voices: %v", err)
	}
	return voices, nil
}

func killTree(pid int) {
	cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	hideChild(cmd)
	_ = cmd.Run()
}
