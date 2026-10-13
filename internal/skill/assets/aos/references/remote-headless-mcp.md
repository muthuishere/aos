# Remote viewing, headless displays, MCP and adapters

## remote: let the person watch (and drive) from a phone

```bash
aos remote share                       # blocks; prints a LAN URL
aos remote share --monitor=2 --fps=8   # one monitor, lower frame rate
aos remote url                         # from another shell: the running share's URL
```

- It shares the screen *and* mouse/keyboard, LAN only, until interrupted. Run
  it in the background and hand the person the URL from `remote url` (exit 1
  when nothing is running).
- `--port` picks the port; `--key` sets the access key.
- Anything that puts the desktop on a public URL (a tunnel) needs the person's
  explicit yes every time, after saying plainly that anyone with the link can
  see and control the machine.

## headless: a display for a server (Linux)

```bash
aos headless status                    # is there a display, and where from
aos headless start --wm                # virtual display (default :99) plus a window manager
aos headless start --display=:101 --size=1920x1080 --wm
aos headless run aos window list       # run something against that display
aos headless stop
```

`start`/`stop` are Linux only (exit 2 elsewhere). Once started, later aos
commands adopt the display automatically, so the GUI commands stop exiting 2.
`--wm` matters: without a window manager, windows cannot be moved or focused.

## serve: give another agent typed tools

```bash
aos serve tools --groups=window        # preview the MCP tool catalogue
aos serve mcp                          # blocks; prints URL + per-run token
aos serve mcp --addr=127.0.0.1:9000 --groups=window,capture,exec
aos serve mcp --gui=off                # screenless tools only
```

Every request needs the token (bearer header or `?t=`); `--token` or
`AOS_MCP_TOKEN` pins it. `--groups` narrows what is exposed: expose the least
the other agent needs. Blocking commands (`watch`, `remote share`, `serve`)
are never tools. To keep it running across reboots:
`aos service create mcp --autostart --now -- aos serve mcp`.

## adapters: add your own commands

```bash
aos adapters path                      # where adapter JSON files are read from
aos adapters example                   # print a starter
aos adapters example --write           # write it there, then edit
aos adapters list                      # what is loaded (--json)
```

An adapter wraps a command line as an `aos <group> <name>` command, shows up
in `aos commands` and over MCP, and can never shadow a built-in. Plugins
(`aos-<group>-<name>` executables on PATH) work the same way.
