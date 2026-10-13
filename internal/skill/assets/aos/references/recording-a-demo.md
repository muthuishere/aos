# Recording a demo, end to end

The playbook for "record a demo of X". It strings together `capture video`,
`subtitle`, `draw` and focus-free input so the result explains itself and the
person's own work is not disturbed. Flag details: `references/capture-video.md`,
`references/draw.md`.

## 1. Ask, and agree the details

Never start a recording on your own initiative. Ask, in one message:

- **Which monitor?** Run `aos display list` and name them ("1 is the laptop,
  2 the external"). Record exactly one, chosen explicitly with `--monitor`.
  Never default to "whatever is focused": the person may be working there.
- **Audio?** `none` (default), `mic` (their narration), `system` (app sound;
  macOS needs a loopback device such as BlackHole, else exit 2), or `both`.
  `aos capture video devices` lists mics; pick one with `--mic=<name|index>`.
- **Where to save?** Default is `~/Movies/aos/` (macOS) or `~/Videos/aos/`.

Warn that everything on that monitor is captured, notifications included.

## 2. Set the stage

Keep the recorded monitor minimal: only the windows the demo needs.

```bash
aos launch Chrome --wait
aos window move Chrome --monitor=2 --zone=1:1      # fill the recorded monitor
aos window list --json                              # check nothing else is there
```

Move unrelated windows off that monitor rather than closing them (they are the
person's). `aos doctor` beforehand catches a missing ffmpeg or Screen Recording
permission (`aos permission request screen`).

## 3. Start

```bash
aos capture video start smart --monitor=2 --audio=mic --max=10m \
    --out=~/Movies/aos/signup-demo.mp4 --json
```

`smart` is the right default: the camera frames the window aos last acted on,
leans toward the cursor, and ignores OS focus, so the person working on
another monitor does not move it. Use `app --app=X` to follow one window,
`mouse --zoom=2` to follow the cursor, `region`/`window` for a fixed crop.
`--max` is a hard cap; a `● REC` caption flashes unless `--indicator=false`.

## 4. Drive it, and narrate on screen

Act through `--app`/`--title` so input goes to the target window without
taking focus, and the camera knows what to frame:

```bash
aos subtitle show "Step 1: open the sign-up page" --seconds=4 --wait
aos key type "https://example.com/signup" --app=Chrome --verify
aos key press enter --app=Chrome
aos capture video mark "sign-up page"
aos draw --app=Chrome --find="Create account" --text="Click here" --duration=3
aos mouse click --app=Chrome --find="Create account" --role=button
aos draw text "Account created ✓" --position=top-center --color=green --duration=3
```

- **`key type ... --app=X --verify`** posts text to that window and reads it
  back (exit 1 if it did not arrive), so the person typing in another app is
  not interrupted. Only add `--focus` when an app ignores posted input; focus
  is handed back after.
- **`mouse click --app=X --find=...`** presses the control directly: no
  pointer jump, no focus change.
- **Subtitles** default to bottom-center (8% up), white on a dark pill; a new
  one replaces the last; `--wait` blocks for `--seconds` so narration and
  action stay in step. `subtitle stop` clears it. `subtitle test` shows a sample to check captions work here.
- **`draw`** arrows point at a control; `draw text` labels without an arrow
  (also bottom-center by default). While recording, both land on the recorded
  monitor unless you target otherwise.
- **Chapters:** `capture video mark "<label>"` at each step; add
  `--rect=x,y,w,h` to make the camera visit that area.
- **Camera overrides:** `capture video focus --app=Chrome --find="Total"
  --zoom=2 --hold=4s` (or `--rect`, `--at=x,y --size=WxH`), then
  `capture video unfocus`. `capture video follow mouse|app|smart|off` switches
  mode mid-recording. `capture video status` checks it is still running.

Pause between steps (a `subtitle ... --wait` does this naturally) so viewers
can follow; a demo driven at machine speed is unwatchable.

## 5. Stop and check

```bash
aos capture video stop --json
```

You get the raw `<out>.mp4`, the zoomed `<out>.final.mp4` and
`<out>.events.json`. Before handing it over:

- Confirm `.final.mp4` exists and is non-trivial (`aos file stat ... --json`).
  If `stop` exited 1 the render failed but the raw file is safe; re-render with
  `aos capture video zoom <out>.mp4` (tune `--max-zoom`, `--ease`, `--pad`,
  `--follow=off`).
- Read `events.json`: each `mark` should be there with a sensible `t`, and
  `duration` should match what you expected. Missing actions mean you drove
  something without `--app`/`--title`.
- Optionally grab a frame to look at:
  `aos exec capture -- ffmpeg -ss 5 -i <out>.final.mp4 -frames:v 1 /tmp/f.png`.

Then tell the person the paths. Do not leave the recording running, and run
`aos draw stop` / `aos subtitle stop` if anything was detached.
