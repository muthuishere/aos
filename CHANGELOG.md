# Changelog

Releases before v0.11.0 are described in their tag messages (`git show v0.10.0`).

## Unreleased

### New: covers (#118, macOS)

- `aos draw cover` blacks out a rect, a window, part of a window (`--rect` in
  points or `N%`) or text found in it (`--find`). Any colour, text that
  shrinks to fit, border, presets (`private`, `redacted`, `brb`, or your own
  under `presets` in the draw config). A window cover follows the window
  (move, resize, other monitor), hides while it is minimised and leaves out
  windows in front of it. Click-through unless `--block-input`. `--detach
  --id` keeps it up until `draw cover stop` / `draw stop`; `cover update`
  changes colour or text in one frame; `cover list` groups them by window.
- `aos screenshot --cover=x,y,w,h | --cover-app | --cover-find` paints the
  covers into the file's pixels and reads them back; live covers are painted
  in too. Fail closed: if a cover cannot be placed, is a tint (alpha < 1) or
  the capture fails, no file is written (exit 3 for not found, else 1).
- Windows and Linux: `draw cover` reports not implemented yet.

## v0.13.1 - 2026-11-09

Fix: aos could bring Chrome to the front and change the tab you were looking at.

- `tab read`, `tab type`, `tab click` and `watch tab` no longer switch tabs.
  They used to select a background tab first, which in practice made the
  browser come forward. Now a tab its window is not showing is refused (exit 3)
  and the message says to run `aos tab focus` first. Add `--focus` to let that
  one command switch to the tab.
- `tab open` is background by default: no window raise, no app activation, your
  current tab stays selected. `--focus` gives the old behaviour; `--background`
  is still accepted and does nothing extra. On Windows a tab cannot be opened
  in the background, so `tab open` there needs `--focus`.
- Only `tab focus` brings a tab forward. `window text` and `find` already stay
  put unless you pass `--raise`. `tab list` is read-only.

## v0.13.0 - 2026-11-09

aos now waits for things as well as doing them: the `watch` set, plus a
handful of small primitives. Principles: aos is primitives. A watch exits 0
when its condition is met and 3 on timeout; the agent does the alerting and the
next step. A command ships only if it works on macOS and Windows. Watching mail,
Teams, WhatsApp or calendar events is `apl`'s job, not aos's.

### New: `watch` (all exit 0 when met, 3 on `--timeout`)

- **`watch tab`** gains `--reload=<dur>` (reload the page between reads),
  `--gone=<text>` (wait for text to disappear) and `--near=<anchor>` (watch only
  the lines around an anchor) (#103).
- **`watch window --app/--title --match/--gone`** waits for text to appear in,
  or vanish from, a native app window without raising it (#106).
- **`watch url <url>`** polls a URL over plain HTTP and reports when its text
  changes, appears or goes away (#111).
- **`watch network`**: online, offline and Wi-Fi changes, `--until=online|offline` (#113).
- **`watch idle`**: exit once the person has been idle for a while, or is active again (#108).
- **`watch lock`**: lock, unlock, sleep and wake events, `--until=` (#114).
- **`watch process`**: wait for a process to exit or appear (#107).
- **`watch battery`**: wait for a charge level or charging state (#109).
- **`watch display`**: wait for a monitor to be plugged in or removed (#112).
- **`watch device`**: USB volumes, audio and Bluetooth devices connected or removed (#110).
- **`watch camera`, `watch mic`**: is the person in a meeting? (#105)

### New: other commands

- **`disk top`**: the biggest folders and files under a path, tagging caches,
  trash and other known space hogs (#94).
- **`display brightness`** works on any monitor, and **`display of`** says which
  monitor a window is on (#95).
- **`launch --or-focus`** raises the app's open window, else launches it (#98).
- **`network captive-portal`** detects a Wi-Fi login portal (#99).
- **`network wifi-qr`** shows a QR code a phone can scan to join the current
  Wi-Fi; the password is opt-in (#101).
- **`capture qr`** decodes QR codes from an image file, a screen region or a
  monitor (#100).
- **`serve files <dir>`** is a read-only static file server: loopback by
  default, `--lan` adds a token in the path, `--for` stops it after a while (#102).

### Changed

- Every command states its host side under WSL and SSH (`windows-host` or
  `local`) in `commands --json`, and the WSL/SSH page carries the full table (#96).
- The agent skill has `references/extending.md`, a guide for adding commands
  through adapters and plugins (#97).

### Fixed

- `window list` no longer marks any window Focused while the Windows desktop
  itself has focus (#104).

### Docs

- Research and ADR 0002 for aos on Android and iOS (#89).
- README, site and skill brought up to date with all of the above.

### Not shipped, on purpose

Per-app volume (#92) was dropped, and `notify wait` and display rotate/scaling
were skipped: neither macOS nor Windows has a public API for them.

## v0.12.0 - 2026-11-08

The machine-facts release: new commands that read or set things a person
would otherwise open Settings for. `menu` and `shortcuts` are macOS only.

### New

- **System:** `system memory`, `system load`, `system uptime`, `system thermal`
  and battery health in `battery status` (#61, #62, #75).
- **Processes and ports:** `process list --sort --top`, `network ports`,
  `process kill --port` (#63, #66).
- **Power and focus:** `power awake` keeps the machine from sleeping (#64);
  `focus` reads and sets Do Not Disturb (#74).
- **Disks:** `disk list`, `disk usage`, `disk eject` (#65); `trash size` and
  `trash empty`, which asks first unless `--yes` (#72).
- **Display:** `display brightness` (#68); `display modes`, `display mode` and
  `display mirror` (#85).
- **Audio and media:** `audio devices`, `audio output`, `audio input` (#70);
  `media now` and play, pause, toggle, next, prev (#76).
- **Network:** `network wifi on|off|join` (#71); `network public-ip` and `vpn` (#78);
  `network speed`, with ping, jitter and download and upload Mbps against
  Cloudflare on a 5 MB budget (#86).
- **Appearance:** dark mode and night light, get and set (#73).
- **Apps and windows:** `app list` (#69); `app hide-others`, `app quit-all` and
  `window desktop` (#77).
- **Bluetooth:** status, paired devices, connect, on and off (#79).
- **Screen and clipboard:** `color at` and `color pick` read a pixel (#80);
  `capture text` does OCR of a region or a monitor (#82); an opt-in
  `clipboard history` recorder with list, get, restore and clear (#87).
- **Files:** `file recent` from the OS's own recents (#81); `file search`
  through the OS index, with a parallel walk as fallback (#88).
- **Other:** `notify` posts a native desktop notification (#67); `shortcuts`
  lists and runs macOS Shortcuts (#83); `menu list` and `menu run` search and
  run an app's menu bar commands on macOS (#84).

### Changed

- The local folder and ctx-optimize store were renamed from `os-cli` to `aos` (#60).

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
