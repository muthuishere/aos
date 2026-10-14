# The host: WSL and SSH

Screen commands run on the **host**, the machine whose screen the person sees.
Everything else runs where you are. Output, JSON and exit codes are identical,
so write commands the same way in every case.

| Follows the host | Always local |
|---|---|
| window, display, mouse, key, find, draw, subtitle | exec, process, file, pkg, service, watch file |
| capture / screenshot, clipboard, watch clipboard, watch window | network, battery, power, system info |
| launch, open, webapp, remote, audio, permission, font | skill, adapters, obs, serve, headless, debug, doctor |
| system lock, system sleep | system logout / restart / shutdown: never forwarded |

## First: find out where you are

```sh
aos doctor | grep host
#  ok   host   local (via auto)                                  -> this machine
#  ok   host   windows-host (/mnt/c/…/aos.exe, via auto: WSL)    -> the Windows desktop
#  ok   host   /tmp/aos-you.sock (via auto: SSH)                 -> the person's laptop
```

Tell the person which screen you are about to act on when it is not this machine.

## WSL

Nothing to set up if `install.sh` (or npm) ran inside WSL. It also installs
`aos.exe` into `%LOCALAPPDATA%\aos`. Paths are translated with `wslpath`, so
`aos screenshot --out=~/s.png` lands in WSL.

If it fails:
- `aos.exe was not found`: rerun `install.sh` inside WSL.
- `Windows programs cannot be started from here`: interop is off. Set
  `[interop] enabled=true` in `/etc/wsl.conf`, then run `wsl --shutdown`.
  Ask the person first.
- `--host=local` acts on WSL's own Linux desktop instead.

## SSH (you are on a server, the screen is the person's laptop)

The person sets this up once. Do not do it for them without asking:
1. Laptop: `aos serve mcp --socket=~/.aos/serve.sock`, or keep it running with
   `aos service create aos-host --autostart --now -- aos serve mcp --socket=~/.aos/serve.sock`.
2. Laptop `~/.ssh/config`, under the server's `Host` entry.
   `ssh_config` does not expand `~` here, so `%d` stands for the home folder:
   `RemoteForward /tmp/aos-%r.sock %d/.aos/serve.sock`
3. Server `/etc/ssh/sshd_config`: `StreamLocalBindUnlink yes`, then restart
   sshd, so each new session can replace the old socket.

Once that is done:
- `--out` / `--preview` files come back to the server path you gave.
- The person sees "aos controlled from <server>". Every call is in their
  `aos obs audit`.
- Refused with a reason:
  - non-screen commands (it is not remote exec)
  - streams: `watch clipboard`, `watch window`, `remote share`
  - video files
  - commands that read a file on the server. For layouts, use
    `aos window arrange - < layout.json`.
- `doctor` says `local` over SSH: the forward is missing. Ask the person to
  check the laptop server and their `RemoteForward` line, and
  `ls -l /tmp/aos-$USER.sock` on the server.
- `not owned by you` / `must be 0600`: someone else made that path. Stop and
  tell the person; do not delete it yourself.

## Override

The first of these that is set wins:
1. `--host=local|windows-host|<socket>`
2. `AOS_HOST`
3. `host = …` in `~/.config/aos/config`
4. auto

An unreachable host is an error, never a silent fallback to the local machine.
