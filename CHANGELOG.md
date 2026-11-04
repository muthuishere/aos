# Changelog

Releases before v0.11.0 are described in their tag messages (`git show v0.10.0`).

## v0.11.0 — speech with subtitles, batch scripts

### New

- **`aos say`** speaks text with the machine's own free voices: `say` on
  macOS, SAPI over COM on Windows (the OneCore voices first, then the desktop
  ones). `--voice`, `--rate` (words per minute), `--out=.mp3|.wav|.aiff|.m4a`
  (a file only; mp3/m4a through ffmpeg), `--async`, `--json`. `say voices`,
  `say wait`, `say stop`. Lines queue, so they never talk over each other.
- **Speech and subtitle go together:** `aos say` shows the same text as an
  aos subtitle while it speaks and clears it when it ends (`--no-subtitle`
  for voice only). It takes every `subtitle show` style flag (`--position`,
  `--size`, `--color`, `--app`/`--title`, ...). Long text is captioned a
  sentence or clause at a time (about two lines), each chunk shown while its
  own audio plays. `subtitle show` stays silent; `subtitle show --say` also
  speaks it.
- **Speak while acting:** `aos say "Now I type the note" -- aos key type
  --app=Notepad "hello"` speaks and runs the command together and waits for
  both; the exit code is the command's. The action starts when the voice is
  heard (not while the speech is still rendering), and `key type` next to a
  voice types word by word over the spoken line.
- An unknown `--voice` is an error (exit 2, with did-you-mean) instead of a
  silent fall back to the default voice.
- **`aos batch`** runs many steps from YAML (a file, `-`, or the YAML inline).
  Steps run in order; `parallel:` runs its list together: say steps first,
  the others once the voice is heard, with typing paced to it. Every aos command is
  a verb (`<group>-<command>`), plus `click`, `type`, `key`, `screenshot`,
  `say`, `wait` and `do:`. A top-level `subtitle:` map sets the caption style
  for every `say:` step. The whole script is checked before step 1 (line:col,
  did-you-mean for verbs and flags); `batch --check` validates only,
  `batch verbs` lists every verb; `--json` gives one result per step with
  its start time (`at_ms`). Spec: `docs/batch-spec.md`; examples in
  `docs/examples/`.

### Fixed

- `launch --wait` waits for the window it launched, not any window of the
  app: with another Notepad already open it used to return at once. A file
  opened as a tab of an existing window (Windows 11 Notepad) counts by title.

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

### Verified on Windows 11 (x64)

Voices (OneCore and SAPI), `say --out` wav/mp3, `say` with subtitle,
`--voice`/`--rate`, an unknown voice failing, `batch` with `parallel`
say + type (words typed through each spoken line, checked on a recording of
the test's own Notepad window), `system info`, `power status`,
`audio volume`/`mute` (read), `process find`, `font list`, `network status`.

### Known gaps

- Not yet run on Windows: clipboard read, full-screen screenshot,
  `audio volume` set on arm64, the rewritten `install.cmd`, and
  `scripts/live-test.cmd`.
