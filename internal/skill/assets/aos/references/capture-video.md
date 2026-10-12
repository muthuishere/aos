# aos capture video: record the screen, get a zoomed cut

The person does the demo and talks; aos records the screen with ffmpeg, logs
what happened, and on stop renders a cut where the camera follows the action.

```bash
aos capture video start smart --audio=mic --out=~/Movies/demo.mp4
#   ... the person talks through the demo; you drive aos commands ...
aos capture video stop
#   raw:    ~/Movies/demo.mp4
#   final:  ~/Movies/demo.final.mp4     <- the zoomed cut
#   events: ~/Movies/demo.events.json
```

## Safety: never record without asking

- **Ask the person, or at least tell them, before `start`.** Recording captures
  everything on the screen, including notifications and passwords as they are
  typed. Never start a recording on your own initiative.
- **Stop when the demo is done.** `--max` (default `10m`) is a hard cap passed
  to ffmpeg as `-t`, so a recording you forget about still ends.
- A `● REC` caption flashes when it starts (`--indicator=false` turns it off).
- Only one recording runs at a time; `start` refuses a second one.

## Modes (the positional after `start`)

| mode | records | camera in the final cut |
|---|---|---|
| `smart` (recommended) | the monitor | frames the active window, leans in toward the cursor while it moves inside a large window, and goes back to the window after 1.5 s idle. `draw`/`find` take over briefly. |
| `monitor` (default) | `--monitor=<n>` (else the focused one) | wide; zooms to each aos action (find, draw, window, key, click, screenshot) |
| `app --app=<name>` | the monitor the app is on | follows that window as it moves and resizes |
| `mouse [--zoom=2]` | the monitor | follows the cursor at a fixed zoom, spring-smoothed with a dead zone; clicks pulse |
| `window --app=<name>` | only that window (fixed crop) | as `monitor`, inside the crop |
| `region --region=x,y,w,h` | only that rect (fixed crop) | as `monitor`, inside the crop |

Camera priority, highest first: explicit `focus` > `draw`/`find` (in smart mode;
every action in monitor mode) > cursor > active window > wide.

## Flags

| flag | meaning |
|---|---|
| `--audio=none\|mic\|system\|both` | default none. `both` mixes the two |
| `--mic=<name\|index>` | which microphone (see `aos capture video devices`) |
| `--fps=30` · `--max=10m` · `--out=<path.mp4>` | rate, hard cap, file (default `~/Movies/aos/` or `~/Videos/aos/`) |
| `--zoom=2` | follow zoom for `mouse` mode |
| `--indicator=false` | no `● REC` caption |
| `--json` | `{Out, PID, Started, Origin, Capture, Mode, Events}` |

System audio: Windows uses ffmpeg's wasapi loopback, else "Stereo Mix" /
virtual-audio-capturer. Linux uses the default sink's PulseAudio monitor. macOS
has no loopback of its own: install one yourself (e.g. `brew install
blackhole-2ch`, BlackHole or Loopback) and route the output to it. Without one,
`--audio=system` exits 2 with that hint. aos never installs anything.

## While recording

```bash
aos capture video mark "login works"                       # a labelled marker
aos capture video mark "the chart" --rect=200,150,800,450  # a marker the camera visits
aos capture video focus --rect=100,100,800,450             # camera goes here...
aos capture video focus --at=640,400 --size=800x450 --hold=4s
aos capture video focus --app=Chrome --find="Sign in" --zoom=2   # same lookup as `aos find`
aos capture video unfocus                                  # ...until this (auto camera resumes)
aos capture video follow mouse|app|smart|off [--app=...]   # switch the live camera
aos capture video status
```

These commands log themselves automatically while a recording runs (a no-op
otherwise): `find` (the match), `draw` (sign plus target, so the arrow and the
thing are both in frame), `window focus` / `window move` (the window after),
`key type` / `key press` with `--app`/`--title` (the window), `mouse click`
(a small box at the pointer), `capture screenshot --app` (the window).

## Stop and re-render

```bash
aos capture video stop            # finalize the mp4 and render <out>.final.mp4
aos capture video stop --raw      # just finalize; render later
aos capture video zoom ~/Movies/demo.mp4 --max-zoom=2 --ease=0.8 --out=demo.cut.mp4
aos capture video zoom demo.mp4 --follow=off   # ignore the cursor/window follow
```

`stop` sends ffmpeg `q` on its stdin so the mp4 is finalized on every OS. If
the render fails, the raw file stays and `stop` exits 1 with the error. Render
again with `zoom`.

`zoom` re-renders an aos recording from its events file. Flags: `--events`,
`--out` (default `<in>.final.mp4`), `--pad=0.15`, `--max-zoom=2.5`,
`--ease=0.6` (seconds), `--hold-wide=1.0`, `--zoom=2`, `--follow`,
`--size=1920x1080`. It is not a video editor; edit the result with your
usual tools.

## The events file (open format)

`<out>.events.json`, version 1:

```json
{"version":1,"out":"/…/demo.mp4","started":"2026-10-12T10:00:00Z","mode":"smart","follow":"smart",
 "monitor":{"X":0,"Y":0,"Width":1512,"Height":982},"capture":{"X":0,"Y":0,"Width":1512,"Height":982},
 "zoom":2,"offset":0.42,"duration":63.1,
 "events":[{"t":3.2,"kind":"action","cmd":"find","rect":{"X":610,"Y":402,"Width":88,"Height":24},"label":"Sign in"}]}
```

`t` is seconds since `started`, and the video begins `offset` seconds later.
`rect`/`at` are global screen points. Map them to video pixels with
`(x - capture.X) * videoWidth / capture.Width`, which also covers Retina,
because avfoundation records physical pixels. Kinds: `action`, `click`, `mark`,
`focus` (+`hold`, `zoom`), `unfocus`, `follow` (label = mode), `cursor` (`at`),
`window`. While recording, the log is `<out>.events.jsonl`, one event per line.

## Setup

`aos doctor` checks for ffmpeg/ffprobe, Screen Recording (macOS) and a loopback
device. A missing ffmpeg exits 2 with `brew install ffmpeg` /
`winget install Gyan.FFmpeg` / `sudo apt install ffmpeg`. On macOS, Screen
Recording belongs to the terminal app (`aos permission request screen`), and
the first `--audio=mic` makes macOS ask for Microphone access.
`aos capture video devices` lists screens, mics and system-audio sources.
