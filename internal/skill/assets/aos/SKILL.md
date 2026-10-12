---
name: aos
description: >
  Drive the machine you are on — macOS, Windows or Linux — through one CLI:
  windows, mouse and keyboard, screenshots, files, processes, shell commands, packages, network,
  audio, clipboard, services, on-screen captions, and a LAN screen-share you can
  watch from a phone. Trigger on: move/resize/focus a window, list windows or
  monitors, take a screenshot, screenshot an app, click, type, press a key
  combination, launch an app, open a URL, read/write/delete a file, run a command
  and capture its output, install a package, check wifi or battery or volume,
  lock or sleep the machine, list or kill a process, watch a file or the clipboard,
  run something as a service,
  show a caption on screen, share my screen on the LAN, what can this machine do,
  snap a window to a half / quarter / third, N-way split, put an app on monitor 2,
  apply / save a window layout, which monitor is active, find a button / menu /
  text on screen, where is the Allow button, point at it, draw an arrow, show me
  where to click, highlight / circle / box this, open a terminal and run a command,
  is this machine set up correctly, expose this machine to an agent over MCP.
---

# aos

> **Supersedes `window-ctl-skill`.** aos runs windowctl as its engine library,
> so everything that skill did (window layout, zones, pointing, find, draw, the
> visual loop) is here as `aos …` commands. If both skills are installed, use
> this one; `windowctl` remains only as the library aos is built on.

One command surface over the machine, identical on macOS, Windows and Linux.
You already have a shell, so just run these commands — there is nothing to start
and nothing to connect to. (The same commands are also available over MCP for
agents that want typed tools; either way it is one code path.)

## The one rule

**Ask the binary, do not guess.** The catalogue is machine-readable and always
current:

```bash
aos commands            # what this machine can run right now
aos commands --json     # the same, parseable, with needs_display + platforms
aos <group> --help      # a group's commands
aos <group> <cmd> --help
```

If a command is not in `commands`, it does not exist here. Do not invent flags —
`--help` lists the real ones, and an unknown flag is rejected rather than ignored.

## Read the exit code

- **0** — worked.
- **1** — ran and failed, or a deliberate "no": `battery present` on a desktop,
  `network wifi` on ethernet, `remote url` with nothing running. Often not an error.
- **2** — refused before doing anything: the command needs a display this machine
  lacks, or is not supported on this platform. The message says which.
- **3** — not found: `find` / `draw --find` could not find the window or the text.
  The message lists the closest labels it did see; retry with one of them.

Exit 2 on Linux is fixable: `aos headless start --wm` provides a virtual
display, and later commands adopt it automatically. On macOS and Windows it means
there is no logged-in session and the desktop commands cannot work at all.

## By intent

| you want to | run |
|---|---|
| see the machine | `system info`, `doctor`, `display list`, `network status`, `battery status` |
| find a window | `window list`, `window list --app=Chrome`, `window list --json` |
| place a window | `window move Chrome --zone=1B` · `--at=x,y,w,h` · `--monitor=2` · `window resize Chrome --w=900 --h=500` |
| save / restore a layout | `window save P` captures every window · `window arrange P` (or `-` for stdin JSON) puts them back |
| focus / wait | `window focus Chrome` · `launch Chrome --wait` · `window wait Chrome --timeout=5000` |
| capture | `capture screenshot` · `--monitor=2` · `--app=Chrome` · `--region=x,y,w,h` · `--out=path` |
| find a control by name | `find --app=X --text=Allow --role=button --json` → Bounds + Center (exit 3 = not there) |
| point the human at it | `draw --app=X --find=Allow --text="Click Allow"` · `draw --at=x,y` · `--rect` · `--mouse` · `--detach` + `draw stop` · `--preview=out.png` |
| click and type | `mouse move X Y` · `mouse click X Y --double` · `key type "text" --app=Chrome` · `key press cmd+shift+s` |
| files | `file read P --lines=10:40` · `file write P text` · `file list D --json` · `file stat P` · `file delete P --recursive` |
| run something | `exec capture -- <cmd>` (JSON: stdout, stderr, exit) · `exec run` (streams) · `exec shell "a \| b"` |
| running processes | `process list --json` · `process find X` · `process kill <pid\|name>` — kill by name refuses when ambiguous |
| packages | `pkg install X` · `search` · `list` · `upgrade` — one verb set over brew, winget, scoop, choco, apt, pacman, yay, dnf |
| keep it running | `service create NAME --autostart --now -- <cmd>` · `start` · `status` · `stop` · `remove` · `list` |
| tell the human | `subtitle show "what I am doing" --seconds=10` — a caption that never steals focus |
| let them watch | `remote share --monitor=2` — prints a LAN URL; the person opens it on a phone |
| react to changes | `watch clipboard --max=1` · `watch window --max=1` · `watch file P --recursive --max=1` — one JSON line per event |

`--app` and `--title` both select a window; a bare word is treated as an app name
and falls back to matching the title, so `window focus Chrome` works everywhere.

## Deeper references

Load the one that matches the intent:

- `references/windows-and-layout.md` — window list, monitors (ACTIVE/FOCUSED),
  zones `1A..2D` and `N:M` splits, move/resize/focus rules, layouts, recipes
- `references/automation.md` — the visual loop, focus guard, open a terminal and
  run a command, screen sharing and the public-tunnel confirmation rule
- `references/find.md` — `aos find`: sources, ranking, roles, exit 3
- `references/draw.md` — `aos draw`: targets, look, `--find`, detach/stop, verified pointing
- `references/permissions.md` — macOS Accessibility + Screen Recording

## Screenshots are for looking, not just saving

`capture screenshot --app=X --out=/tmp/x.png` prints the captured rect
(`900x500+100+100`). Read the PNG back to see what is on screen before clicking —
that rect is how image coordinates become click coordinates: global = origin + pixel.

## Things that will bite you

- **`exec capture` runs a program, not a shell.** `echo` is a builtin on Windows;
  use `exec capture -- cmd /c echo hi`, or `exec shell` when you want pipes and
  globs. Put `--` before the child command so its flags are not read as ours.
- **`launch` passes unknown flags to the app**, on purpose. Only `--wait` is ours.
- **A blocking command never returns**: `watch`, `remote share`,
  `serve mcp`. Give them `--max=N` where it exists, or run them in the background.
  They are deliberately absent from the MCP tool list.
- **Public screen sharing needs an explicit yes.** `remote share` is LAN-only.
  Anything that puts desktop control on a public URL (e.g. `windowctl remote
  --tunnel`) must be confirmed by the human every time, after saying plainly
  what it exposes.
- **`draw` blocks** for its `--duration` (default 8 s) unless `--detach`; clean
  up detached drawings with `draw stop`.
- **`file delete` refuses** a filesystem root, `$HOME`, and system directories like
  `/etc`. That is a guard, not a bug. Directories need `--recursive`.
- **Nothing is silently unavailable.** A command that cannot run here still appears
  in `commands --all`, marked, and explains itself when invoked.

## Checking the machine

```bash
aos doctor          # clipboard round trip, real screenshot, window backend,
                           # permissions, package manager — each with a fix
```

On macOS, denied permissions are the usual cause of blank screenshots or failing
input: `aos permission request`.

## Driving a different machine

Nothing here is tied to a connection, so any machine you can `ssh` to and that
has the binary works the same way — same commands, same exit codes:

```bash
ssh server aos system info
ssh server aos exec capture -- systemctl is-active nginx
ssh server 'go install github.com/muthuishere/aos/cmd/aos@latest'
```

A server has no display, so the desktop commands there exit 2 with that reason.
On Linux, `ssh server aos headless start --wm` gives it one and later commands
adopt it. The audit trail lives on the machine that ran the command, so
`ssh server aos obs audit` is how you see what happened *there*.

## Serving it to another agent

```bash
aos serve mcp        # prints the URL and a per-run token; both are needed
```

The server refuses any request without the token, as a bearer header or `?t=`.
Loopback is not a permission. `AOS_MCP_TOKEN` pins one across restarts.

GUI tools are exposed only when the machine has a display (`--gui=on|off|auto`),
because offering a tool that cannot run is worse than not offering it.

## Adding commands

Two ways, both discovered automatically and both exposed over MCP:

- **Adapter** — JSON in `~/.config/aos/adapters/`, wrapping a command line.
  `aos adapters example --write` writes a starter.
- **Plugin** — any executable named `aos-<group>-<name>` on PATH, describing
  itself in `# aos:summary=` comment headers.

Neither can shadow a built-in command.

## What was asked of this machine

```bash
aos obs audit --since=24h   # what was run, oldest first — the audit trail
aos obs audit --source=mcp  # only what agents asked for
aos obs summary             # is this being used, and by what
aos obs stats               # p50/p95 and failure rate per route
aos obs export              # OTLP JSON
```

Spans record the route, exit code, duration and the *count* of arguments — never
their contents.

## The skill keeps itself current

Every aos command (except `skill install/uninstall` and help) checks that this
skill is installed and matches the binary, and refreshes it if not, saying so in
one stderr line. `AOS_NO_SKILL_AUTOINSTALL=1`, or `aos skill uninstall`, turns
that off; `aos skill install` turns it back on.
