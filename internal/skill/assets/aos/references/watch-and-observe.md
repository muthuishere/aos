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
```

- `watch tab` prints only the new lines of the page (`"added":[...]`). With
  `--match` it exits 0 when matching text appears and 3 on `--timeout`. Text
  already on the page at start does not count. See `references/tabs.md`.
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
