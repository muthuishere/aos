# Watching for changes, and seeing what was done

## watch: react to the machine

Each `watch` command prints one JSON line per change and runs until stopped.
An agent almost always wants `--max=N` so it returns:

```bash
aos watch clipboard --max=1 --content     # wait for the next copy; --content includes the text
aos watch window --max=1                  # wait until the focused window changes
aos watch file ./notes.md --max=1         # wait for one change to a file
aos watch file ./src --recursive --interval=1s
aos watch tab --id=ID --match="Done" --timeout=5m   # wait for text to appear in a browser tab
aos watch tab --url=x --reload=30s --match="Book now" && aos tab click --url=x --find="Book now"
aos watch tab --url=x --reload=1m --gone="Sold out" --timeout=2h
```

- `watch tab` prints only the new lines of the page (`"added":[...]`). With
  `--match` it exits 0 when matching text appears, with `--gone` when the text
  disappears, and 3 on `--timeout` or when the tab closes. Text already on the
  page at start does not count for `--match`. `--reload=<dur>` (min 10s,
  jittered) reloads the tab between reads; `--near=<anchor>` watches only the
  lines around an anchor. See `references/tabs.md`.
- `watch window` with `--app` (or `--title`) and `--match` / `--gone` waits for
  text in a native app window instead of reporting focus changes: exit 0 when
  the text is in the window (`--match`) or no longer there (`--gone`), 3 on
  `--timeout`. It reads the window like `aos window text` (accessibility, OCR
  fallback) without raising it. `--new` makes `--match` ignore text that was
  already there at the first read; `--exact` wants a whole line; `--interval`
  defaults to `2s` here. No `--json` needed to chain; with it the exit prints
  one `window-text` line naming the window and the matching line.
- A watch only waits. Alerts, notifications and "then do X" are yours: chain
  with `&&` on exit 0, branch on exit 3.
- `--interval` is the poll period (default `500ms`).
- Without `--max` they block forever: run them in the background and read the
  stream, or do not run them at all. They are left out of MCP for that reason.
- `watch clipboard` without `--content` reports *that* it changed, not what
  was copied. Ask for the text only when you need it; it may be a password.
- `watch clipboard` and `watch window` need a display.

### watch camera / watch mic: is the person in a meeting?

`watch camera` and `watch mic` say whether any app is using the camera or the
microphone. A busy camera or mic is the best cheap sign that the person is on
a call, so wait on it before noisy work and carry on after. They follow the
host under WSL (the camera that matters is the one at the person's desk).
macOS and Windows only.

```bash
aos watch camera --until=free && ./start-the-noisy-build.sh   # wait for the call to end
aos watch mic --until=busy --timeout=10m                      # wait for a call to start
aos watch camera                       # stream: current state, then each change
aos watch mic --json                   # {"event":"mic","state":"busy","apps":["us.zoom.xos"],...}
```

- `--until=busy|free` exits 0 as soon as that state is true (at once if it
  already is) and exits 3 on `--timeout`. Do the follow-up work yourself after
  `&&`; there is no `--then`.
- Without `--until` it is a stream and `--timeout` just ends it with 0.
- Reading the state never opens the device: no privacy light, no permission
  prompt.
- Who: the microphone names the app on Windows and on macOS 14.2+ (bundle id);
  the camera names the app on Windows only. On macOS the camera line shows the
  device name instead, because the OS gives no public per-app answer.
- Windows reads the privacy consent store, so an app that crashed while
  holding the device can read as busy until it is used again.

Typical uses: "tell me when the download lands" (`watch file ~/Downloads
--max=1`), "do X after I copy the link" (`watch clipboard --max=1 --content`),
"wait until I switch to the browser" (`watch window --max=1`).

### watch battery: save work before it dies, start when it is full

`watch battery` is a gate, not an alerter: exit 0 when a condition holds, 3 on
`--timeout`, and the agent decides what to do.

```bash
aos watch battery --below=20 && <agent saves work>     # exit 0 once under 20%
aos watch battery --until=full --timeout=3h             # charge finished (3 if not in 3h)
aos watch battery --until=discharging                   # the charger was pulled
aos watch battery                                       # JSON line per plugged, unplugged, full, level
```

- Conditions are states, not edges: `--below=20` at 15% returns at once. With
  several (`--below`, `--above`, `--until`), the first that holds ends the wait.
  `--below`/`--above` are strict (`--below=20` means 19% or less).
- Streaming (no condition) lines carry `percent`, `status` and `on_ac`.
- No battery (a desktop): exit 2 with a message, before any waiting.
- Polls every 5s by default (`--interval`). `--json` for the line on exit.
- Follows the same side as `battery`: the machine `aos` runs on.
### watch idle: is the person at the desk

```bash
aos watch idle --for=10m && <agent does the noisy work>   # exit 0 once idle that long
aos watch idle --active --timeout=1h && <agent stops>     # exit 0 on the next key or mouse move
aos watch idle --for=5m --json                            # {"event":"idle","state":"idle","idle_ms":...}
```

Idle time is keyboard and mouse input idle (macOS HIDIdleTime, Windows
GetLastInputInfo, Linux needs `xprintidle` on X11). `--for` also exits at once
when the person is already idle that long. `--active` takes the first sample as
the baseline and exits when the idle counter drops. Exit 3 on `--timeout`, 1
when the machine cannot report idle time. It follows the host under WSL.
### watch process: a build, render or download finishes

`watch process` is a gate, not an alerter: it exits 0 when its condition is
met, 3 on `--timeout`, and the agent decides what happens next.

```bash
aos watch process --name=ffmpeg --exit && <agent acts>      # render finished
aos watch process --pid=4821 --exit --timeout=30m --json   # {"event":"stop","pid":4821,...}
aos watch process --name=Xcode --start                      # wait for it to appear
aos watch process --name=node                               # JSON line per start and stop
```

- `--name` is the whole process name, case-insensitive (`sleep` does not match
  `sleepd`); `--pid` is exact. Give one of them.
- `--exit` exits 1 at once if nothing matches: it cannot wait for something
  that is not running. With `--name` it waits for every process that matched
  at the start; ones that start later do not hold it open.
- `--start` needs `--name` and ignores processes already running.
- `exit_code` appears in the output only on Windows; macOS and Linux give no
  exit code for a process that is not our child.
- Local to the machine `aos` runs on (like `process`), never forwarded.
### Chaining: the watch is the condition, the agent is the action

aos has no `--notify` or `--then`. A watch exits 0 when its condition is met,
so the agent chains what happens next:

```bash
# a Teams or Slack message from Muthu showed up (no notification API needed)
aos watch window --app=Teams --match="Muthu" --new --timeout=1h && <agent reads and replies>
aos watch window --app=Notes --gone="Syncing" --timeout=2m || echo "still syncing"
```

Exit 3 is the timeout, so `&&` runs the action only when the text arrived.
Read the full thread afterwards with `aos window text Teams`.

## obs: what this machine was asked to do

Every command run, from the CLI or over MCP, is recorded locally as a span:
route, exit code, duration and the *count* of arguments, never their contents.

```bash
aos obs audit --since=24h            # every command, oldest first
aos obs audit --source=mcp           # only what agents asked for
aos obs audit --failed
aos obs tail --limit=20              # most recent spans, newest last
aos obs stats --since=24h --source=mcp   # calls, failures, p50/p95 per route
aos obs summary --since=7d           # is this used, and by what
aos obs export --since=1h > spans.json   # OTLP JSON for a collector
aos obs path                         # where the telemetry files live
```

`--since` takes durations like `1h`, `24h`, `7d`; `--json` on audit/stats/
summary for parsing. Use `obs tail` right after a failure to see the exit code
and timing of what just ran, and `obs audit --source=mcp` to answer "what did
the other agent do on this machine?". On a remote box, run it there:
`ssh server aos obs audit`.
