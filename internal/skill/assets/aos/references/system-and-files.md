# System, files, processes and the rest of the machine

None of these need a display unless marked, so they work the same over `ssh`
on a headless server. Every one has `--help`; trust it over memory.

## Destructive: ask the person first

These change or end things the person cares about. Say what will happen and
get a yes before running them, every time, even if a similar one was approved
earlier:

| command | what it does |
|---|---|
| `system restart` · `system shutdown` | reboots / powers off the machine — your session dies with it |
| `trash empty --yes` | permanently deletes everything in the Trash / Recycle Bin; without `--yes` it refuses away from a terminal |
| `system logout` | logs the user out, closing their apps (unsaved work is lost) |
| `system sleep` · `system lock` | less harmful, but still takes the screen away from the person |
| `process kill <pid\|name> [--force]` · `process kill --port=<n>` | ends a process (or every process listening on the port); `--force` skips the graceful signal |
| `file delete <path> [--recursive]` | no trash, no undo |
| `pkg remove <pkg...>` · `pkg upgrade` | changes installed software machine-wide |
| `service remove <name>` | stops a service and deletes its definition |
| `file write` / `file move` onto an existing path | overwrites it |

`process kill Calculator` by name refuses when more than one process matches;
use `process find Calculator` and kill by pid. `file delete` refuses `/`,
`$HOME` and system directories on purpose.

## Files

```bash
aos file read main.go --lines=10:40     # print a range
aos file write notes.txt "first line"   # or: cat in.txt | aos file write out.txt
aos file append log.txt "done"
aos file list ~/Downloads --json        # --all includes dotfiles
aos file stat go.mod --json             # size, mode, modified
aos file mkdir ~/.config/aos/layouts    # with parents
aos file copy a.txt b.txt
aos file move old.txt new.txt
aos file delete /tmp/scratch --recursive
aos open .                              # default handler (display); also URLs
```

## Clipboard

```bash
echo hello | aos clipboard copy         # or: aos clipboard copy "some text"
aos clipboard paste > note.txt
```

The clipboard is the person's. Copying over it loses what they had; paste it
first if you will need to restore it.

## Running commands

```bash
aos exec capture -- git status          # JSON {stdout, stderr, exit}: best for agents
aos exec run git status                 # streams output live, exit code passes through
aos exec shell "git log --oneline | head -5"   # pipes, globs, builtins
```

`exec capture` and `exec run` run a program, not a shell; put `--` before the
child's flags. On a Linux box with a virtual display,
`aos headless run <cmd>` runs it against that display.

## Processes and services

```bash
aos process list --filter=chrome --json
aos process list --sort=mem --top=5   # what is eating memory (--sort=cpu|mem|name|pid)
aos process find node --json
aos process kill 4821                   # confirm first
aos process kill --port=8080            # whoever listens on 8080; confirm first

aos service create mcp --autostart --now -- aos serve mcp
aos service start mcp · aos service stop mcp
aos service status mcp                  # non-zero exit when not running
aos service list
aos service remove mcp                  # confirm first
```


## Packages

```bash
aos pkg manager            # homebrew, winget, apt, ...
aos pkg search ripgrep
aos pkg install ripgrep    # installs software: say so before doing it
aos pkg list
aos pkg refresh            # update the index
aos pkg upgrade            # everything; confirm first
aos pkg remove ripgrep     # confirm first
```

## Hardware and state

```bash
aos system info            # OS, host, hardware
aos trash size [--json]    # items and bytes in the Trash / Recycle Bin
aos system load --json     # CPU busy % overall + per core, load averages (none on Windows)
aos system uptime          # up 9d 14h 8m (since …)
aos system memory --json   # total / used / available / free RAM, swap, pressure (macOS)
aos battery status · aos battery percent    # 87
aos battery present && echo laptop          # exit 1 on a desktop: not an error
aos power status           # ac | battery
aos disk list --json · aos disk usage [path]   # free / total per volume
aos disk eject /Volumes/USB   # removable only; refuses / and C:
aos power awake --for=2h [--display]   # keep awake; `power awake off` / `power awake status`
aos network status · aos network ip · aos network wifi   # wifi exits 1 on ethernet
aos network wifi on · aos network wifi off   # confirm before off: it drops the connection
printf '%s' "$PW" | aos network wifi join <ssid> --password-stdin   # never put a password on argv
aos network ports [3000] [--json]   # listening TCP ports → pid, process; exit 1 if the port is free
aos audio volume · aos audio volume 40 · aos audio volume +5
aos notify "Build finished" "all 212 tests green" [--sound]   # native notification to the person
aos audio mute toggle      # on | off | toggle; bare prints the state
aos audio devices --json   # outputs and inputs, * = default
aos audio output AirPods · aos audio input "MacBook Pro Microphone"   # switch by name/id; bare prints current
aos font list mono · aos font dirs
```

Changing volume or mute affects the person; only do it when asked (or when
setting up a recording, and say so).

## The tool itself

```bash
aos version
aos doctor [--json]        # each failed check comes with its fix
aos debug                  # diagnostic dump for a bug report
aos debug tools            # which helper tools are installed
aos completion zsh         # bash | zsh | fish
aos skill show             # print this skill; skill path shows where it installs
aos skill install · aos skill uninstall
aos webapp open https://app.slack.com   # URL in a chromeless app window (display)
```
