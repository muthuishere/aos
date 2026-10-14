# ADR 0001: GUI commands follow the screen ("host")

- Status: **Proposed**, for discussion
- Date: 2026-10-14

## Context

aos is GUI-first. An agent's shell is often not on the machine with the screen:

| Where the agent's shell runs | Where the screen is |
|---|---|
| a Mac, Windows or Linux desktop | the same machine |
| WSL | the Windows host |
| an SSH session opened from a Mac | the Mac |

Today aos running inside WSL acts on WSL. Its Linux desktop is either missing or WSLg, which is not what anyone sees. Over SSH it finds no display at all. Both cases work today only if the user runs a second aos by hand and connects the two.

What we already have to build on:
- **WSL can launch Windows programs.** Running `aos.exe` from inside WSL starts it on Windows, and stdout, stderr and the exit code pass back.
- **`aos serve mcp`** already exposes every command as a tool.
- **SSH can forward a Unix socket** back to the client with `-R`. ssh-agent and X11 forwarding work this way.

Constraints:
- **aos stays small.** No new protocols or dependencies, no embedded RDP, VNC or tailnet.
- **Agentless.** Nothing new runs on a machine beyond aos itself.
- **No machine-to-machine remote control.** Remote exec is SSH's job and agents already handle it. That idea was dropped on 2026-10-14.

## Decision

Introduce one concept: the **host**, meaning the machine whose screen the user is looking at.

**Screen commands** run on the host. **Commands about the agent's own machine** always run locally. Delegating to the host is invisible: the command line, JSON output and exit codes are identical either way.

### 1. Which commands follow the host

So it is **not only Windows-related commands**. It is everything that touches the screen, input or the person sitting at it, on any OS.

| Follows the host (screen and person) | Always local (agent's machine) |
|---|---|
| `window`, `display`, `mouse`, `key`, `find` | `exec`, `process`, `file`, `pkg`, `service` |
| `draw`, `subtitle` | `watch file` |
| `capture` (screenshots and video) | `network`, `battery`, `power`, `system info` |
| `clipboard`, `watch clipboard`, `watch window` | `skill`, `adapters`, `obs`, `serve`, `headless` |
| `launch`, `open`, `webapp` | `debug`, `completion`, `version` |
| `remote share` / `remote url` | `doctor`: runs locally and reports the host too |
| `audio`, `permission`, `font` | |
| `system lock`, `system sleep` (always the host: they mean "lock *my* screen"; the reply is sent before the machine sleeps, since sleeping the Mac drops the SSH session) | `system logout`, `system restart`, `system shutdown`: local only, never forwarded |
| `tab` (future) | |

`--host=local|<name>` on any command overrides the table.

### 2. How the host is chosen (first match wins)

1. the `--host=…` flag
2. the `AOS_HOST` environment variable (a name, `local`, or a socket path / URL)
3. the config file: `~/.config/aos/config` with `host = auto | local | windows-host | <socket/url>`
4. **auto**:
   - In WSL with Windows launching enabled, the host is `windows-host`.
   - In an SSH session (`SSH_CONNECTION` is set) where `/tmp/aos-$USER.sock` exists and answers, the host is that socket.
   - Otherwise the host is `local`.

### Detection

| Case | Signal | How the host is located |
|---|---|---|
| WSL | `WSL_DISTRO_NAME` or `WSL_INTEROP` is set, or `/proc/sys/fs/binfmt_misc/WSLInterop` exists | Read `%LOCALAPPDATA%` (`cmd.exe /c echo %LOCALAPPDATA%`, then `wslpath`) to find `aos.exe`. Cache the path in the config. |
| SSH | `SSH_CONNECTION` is set. It holds the client's IP, but that can't be used to reach the client. | Look for the forwarded socket at `/tmp/aos-$USER.sock` and send it a ping. |
| Neither | — | local |

SSH alone never points back to the client. Without the forwarded socket, aos runs locally (and has no display on a headless server), and the error message tells the user to add the `RemoteForward` line.

### 3. How commands reach the host

| Host | Mechanism | New code |
|---|---|---|
| `local` | as today | none |
| `windows-host` | run `aos.exe` with the same arguments. Path arguments and the current directory go through `wslpath -w`, so output files land back in WSL. | the dispatcher plus path translation |
| socket (SSH) | the client side of the existing `serve mcp`, over a Unix socket | an MCP client call; the server already exists |

**SSH setup.** On the Mac, `aos serve mcp --socket ~/.aos/serve.sock` runs, as a login item or started by hand. Add one line to `~/.ssh/config`:

```
RemoteForward /tmp/aos-%r.sock ~/.aos/serve.sock
```

No `SetEnv` is needed: sshd rejects unknown variables unless the server allows them, so aos looks for the socket at the conventional path instead (see *Detection*).

Because the socket is forwarded, no network port is ever opened.

### 4. Installing

- **WSL:** `install.sh` and the npm postinstall step detect WSL and also put `aos.exe` in `%LOCALAPPDATA%\aos\`. One install covers both sides.
- **SSH:** install aos normally on both machines.
- **Versions:** `aos doctor` warns when the local and host versions differ.

### 5. Failure

If the host can't be reached (aos.exe missing, Windows launching disabled, or the socket gone), the command **fails with the exact fix**. It never silently falls back to local, because a screenshot of the wrong screen is worse than an error.

## Consequences

**Good**
- An agent in WSL or over SSH controls the screen the person is actually looking at, with no flags.
- No new protocols or dependencies, so aos stays small: about 200–300 lines plus installer changes.
- The SSH case reuses `serve mcp` and SSH's own authentication. No tokens on the network.

**Costs and risks**
- Each WSL call starts a Windows process, which adds latency (not yet measured).
- Over SSH, anyone with access to the forwarded socket on the server can drive your Mac. The mitigations:
  - the socket is created `0600` and per user (`%r`)
  - forwarding is opt-in through ssh config
  - the host shows a caption, "aos controlled from <host>", while it is being driven
  - `obs audit` logs every forwarded call
- The command table becomes a contract. New command groups must declare which side they belong on.

## Alternatives considered

- **Embedding an RDP, VNC or tailnet client:** rejected. It makes aos big, and the remote machine needs something running.
- **A generic `--on=<host>` remote control with contexts, like Docker or kubectl:** rejected. It's SSH's job and agents already do it.
- **Requiring the user to always pass `--host`:** rejected as the default. Agents forget flags, so auto-detection is the point. The flag remains as an override.

## Open questions

0. `aos doctor` already exists. It should gain one `host:` line, with no new command.
1. Should the table live in code (each group declares `FollowsHost`) or in config?
2. ~~lock/sleep~~ Decided: always the host.
3. `watch window` and `watch clipboard` stream output. Does the WSL path carry the stream directly, while the socket path needs MCP streaming?
4. Should the "controlled from <host>" caption always be on, or be configurable?
5. Should Linux desktops reached over SSH, and Windows reached over SSH, both work as SSH targets?
6. ~~Name~~ Decided: `host` (`--host`, `AOS_HOST`, `host =` in config).
