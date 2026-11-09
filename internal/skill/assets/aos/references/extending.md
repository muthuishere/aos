# Extending aos: add your own commands

Use this when the person wants a command aos does not ship. Do not ask them to
patch aos. Both ways below show up in `aos commands`, `--help` and as MCP
tools, and neither can replace a built-in (a built-in always wins).

Pick one:
- **Adapter**: a JSON file, no code. Use it to wrap a command line they already run.
- **Plugin**: any executable. Use it when the command needs logic: a script or a program.

## Adapter (JSON)

```bash
aos adapters path              # where they live (~/.config/aos/adapters)
aos adapters example --write   # write a working starter there, then edit it
aos adapters list              # what this machine has loaded
aos commands --check           # lint after every edit; non-zero = something is off
```

`~/.config/aos/adapters/notes.json`:

```json
{
  "group": "notes",
  "description": "Personal notes",
  "commands": [
    {
      "name": "today",
      "summary": "Open today's note",
      "args": "[<topic>]",
      "examples": ["aos notes today", "aos notes today standup"],
      "run": "$EDITOR ~/notes/$(date +%F).md",
      "platforms": ["darwin", "linux"]
    }
  ]
}
```

| field | meaning |
|---|---|
| `group`, `description` | the group word, and its line in help |
| `name`, `summary` | the command word, and one line of help |
| `args` | the usage shown in help, e.g. `<text...>` |
| `examples` | **required**: `aos commands --check` fails without them |
| `run` | a shell line. Arguments typed after it are appended, quoted. |
| `env` | extra environment variables |
| `platforms` | `darwin`, `windows`, `linux`; omit this field to run on all |
| `aliases`, `hidden`, `needsDisplay`, `blocking` | optional |

## Plugin (executable)

Any executable named `aos-<group>-<name>` in `$AOS_BIN_DIR`, in
`~/.config/aos/bin`, or on `PATH` becomes `aos <group> <name>`. It describes
itself in comment headers within its first 80 lines:

```sh
#!/bin/sh
# aos:summary=Deploy the current branch
# aos:args=<env>
# aos:examples=aos deploy run staging | aos deploy run prod
# aos:platforms=darwin | linux
# aos:route=deploy run   # only needed when a word contains a hyphen
```

Save it as `~/.config/aos/bin/aos-deploy-run`, `chmod +x` it, then run
`aos deploy run staging`.

## Rules

- **Ask before creating** an adapter or plugin in the person's config. It
  stays on their machine and runs as them.
- **No secrets in the file.** Reference `$VAR` in `run`; the shell expands it at run time.
- **Quote carefully.** `run` goes through the platform shell (`$SHELL -c` on macOS and Linux,
  `cmd /c` on Windows), so write a separate command per platform if
  they differ.
- **Run `aos commands --check` after any edit.** A malformed adapter is
  reported there, not silently skipped.
- **Event hooks are not built in.** To react to an event, loop on
  `aos watch window|clipboard|file|tab` and call the command from there.
