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
| `system logout` | logs the user out, closing their apps (unsaved work is lost) |
| `system sleep` · `system lock` | less harmful, but still takes the screen away from the person |
| `process kill <pid\|name> [--force]` | ends a process; `--force` skips the graceful signal |
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
aos process find node --json
aos process kill 4821                   # confirm first

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
aos battery status · aos battery percent    # 87
aos battery present && echo laptop          # exit 1 on a desktop: not an error
aos power status           # ac | battery
aos network status · aos network ip · aos network wifi   # wifi exits 1 on ethernet
aos audio volume · aos audio volume 40 · aos audio volume +5
aos audio mute toggle      # on | off | toggle; bare prints the state
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
