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

Typical uses: "tell me when the download lands" (`watch file ~/Downloads
--max=1`), "do X after I copy the link" (`watch clipboard --max=1 --content`),
"wait until I switch to the browser" (`watch window --max=1`).

### watch display: a monitor is plugged in or removed

`watch display` is a gate, not an alerter: exit 0 when its condition is met, 3
on `--timeout`, and the agent decides what to do. It diffs `display list` on a
poll (`--interval`, default 2s).

```bash
aos watch display --until=added && <agent applies the dock layout>
aos watch display --until=removed --timeout=10m
aos watch display --until=2                  # exactly 2 monitors attached
aos watch display                            # JSON line per monitor added or removed
```

- Each line has `change` (`added`/`removed`), `id`, `name`, geometry and the
  `count` of monitors attached afterwards. `id` is the `display list` number:
  the new one for `added`, the old one for `removed`.
- `name` is the geometry (`2560x1440 at +1728+0`): the listing has no product
  name. Monitors are told apart by geometry, since ids renumber when one is
  unplugged; rearranging or changing resolution reads as removed plus added.
- Monitors present at the start are the baseline, not events. `--until=<n>`
  is a state and returns at once if `n` monitors are already attached.
- Follows the host, like `display` and `window`.

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
