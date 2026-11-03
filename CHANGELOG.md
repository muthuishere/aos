# Changelog

Releases before v0.11.0 are described in their tag messages (`git show v0.10.0`).

## v0.11.0 — built-in narration, batch scripts, window-only recording

### New

- **`aos say`** speaks text with the machine's own free voices: `say` on
  macOS, SAPI over COM on Windows (the OneCore voices first, then the desktop
  ones). `--voice`, `--rate` (words per minute), `--out=.mp3|.wav|.aiff|.m4a`
  (mp3/m4a through ffmpeg), `--async`, `--json`. `say voices`, `say wait`,
  `say stop`. Lines queue, so they never talk over each other.
- **Speak while acting:** `aos say "Now I type the note" -- aos key type
  --app=Notepad "hello"` speaks and runs the command together and waits for
  both; the exit code is the command's.
- **Narrated recordings, no mic, no editing:** while `capture video` runs,
  every `aos say` is time-stamped and `capture video stop` mixes it into
  `<out>.final.mp4` at the moment it was spoken (`--narration=mix|replace|off`).
  `say --narrate` adds a line without playing it aloud. `subtitle show --speak`
  captions and speaks.
- **`aos batch`** runs many steps from YAML (a file, `-`, or the YAML inline).
  Steps run in order; `parallel:` runs its list together. Every aos command is
  a verb (`<group>-<command>`), plus `click`, `type`, `key`, `screenshot`,
  `record`, `record-stop`, `say`, `wait` and `do:`. The whole script is
  checked before step 1 (line:col, did-you-mean for verbs and flags);
  `batch --check` validates only, `batch verbs` lists every verb. Spec:
  `docs/batch-spec.md`; examples in `docs/examples/`.
- **`capture video start windows --app=A[,B]`** (macOS) records only the named
  windows' own pixels through ScreenCaptureKit: windows on top, notifications
  and the desktop never reach the video. One mp4 per window, joined side by
  side at stop. `--dry-run` resolves the windows and records nothing.

### Changed (Windows: no PowerShell)

aos no longer starts PowerShell anywhere; Defender had quarantined aos.exe for
launching a hidden PowerShell. Native Win32/COM code replaces it for speech,
audio volume and mute, clipboard read, full-screen screenshots, `system info`,
process list, power, network and fonts.

- `power status` now reports `charging`, `charged`, `not charging` or
  `discharging`.
- On Windows arm64, `audio volume set` lands on the device's nearest volume
  step (Go cannot pass float arguments to system calls on arm64).
- `subtitle show` on Windows has no WinForms fallback: if the native overlay
  fails, the command fails.
- `.ps1` files are no longer run as plugins.
- `install.cmd` is plain cmd (curl.exe, tar.exe, certutil; SHA-256 still
  verified). On WSL, `install.sh` unpacks the Windows build with `tar.exe`.
- Windows live checks (`test/windows/`, `scripts/live-test.cmd`) are Go
  programs with `.cmd` runners.

### Known gaps

- The Windows changes above have passed `go vet` and cross-compile, but have
  not yet run on a Windows machine.
- `capture video start windows` has been checked with `--dry-run` only; it has
  not yet recorded a real video.
