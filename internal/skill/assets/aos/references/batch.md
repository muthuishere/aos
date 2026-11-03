# Batch: many steps from YAML

`aos batch` runs a list of steps in one process: from a file, from stdin
(`-`), or with the YAML itself as the argument. Steps run top to bottom.
The only flow construct is `parallel`. The whole script is checked before
step 1, so a typo never fails halfway through.

```yaml
steps:
  - launch: calc
  - parallel:
      - say: seven plus two
      - click: Calculator Seven
  - type: Notepad done
  - wait: 2s
```

```bash
aos batch demo.yaml
aos batch --check demo.yaml            # validate only; exit 2 on any error
aos batch --check demo.yaml --json     # findings as JSON: severity, at (line:col), message, hint
aos batch --json demo.yaml             # one JSON line per step (step, run, ok, exit, ms, output) + a summary
aos batch '[launch: calc, parallel: [say: seven, click: Calculator Seven], type: Notepad done]'
aos batch verbs                        # every verb, generated from the commands
```

## Rules

- A step is one verb: `- verb: arguments`, or just `- verb` with none.
- `parallel:` runs its list together; the next step waits for all of them.
  No parallel inside parallel.
- A failure stops the run (exit 1). Check errors stop it before step 1 (exit 2).
- A top-level list, or a map with only `steps:`.

## Verbs

Every aos command is a verb named `<group>-<command>`, taking the same
words and flags as the CLI: `window-list: --app=Notepad`,
`capture-video-mark: chapter one`, `tab-send: --id=T --text=hi`.
Flags are checked against the command's own usage line, with "did you mean".

Short names:

| verb | runs |
|---|---|
| `click: Calculator Seven` | `mouse click --app=Calculator --find=Seven` (`click: 10 20` clicks a point) |
| `type: Notepad hello world` | `key type "hello world" --app=Notepad` (`type: hello` = focused window) |
| `key: Notepad ctrl+s` | `key press ctrl+s --app=Notepad` (`key: enter`) |
| `screenshot: Calculator --out=c.png` | `capture screenshot window --app=Calculator` (no app: the screen) |
| `record: demo.mp4 window --app=TextEdit` | `capture video start window --app=TextEdit --out=demo.mp4` |
| `record-stop` | `capture video stop` (+ its flags, e.g. `--narration=replace`) |
| `say: Hello there --voice=Zira` | speaks and shows the text as a subtitle while it plays (`--no-subtitle` for voice only); while recording, voice and caption land in `.final.mp4` in sync |
| `wait: 2s` | pause (`500` = ms, `2s`, `1m`) |
| `do: window focus Notepad` | any aos command as typed (the escape hatch) |

Long form: `click: {app: Calculator, target: Seven}`; other verbs take
flags as map keys (`window-list: {app: Notepad}`) and positionals as `args:`.

System-level groups (`power`, `system`, `pkg`, `service`, `network`) are
not verbs; reach them with `do:` and the check warns.

## Narrated demo

Speech and action in one `parallel` keep the voice in step with the screen:

```yaml
steps:
  - record: ~/Desktop/demo.mp4 window --app=TextEdit --audio=none
  - wait: 1s
  - parallel:
      - say: I type a note while I talk.
      - type: TextEdit Hello from aos.
  - parallel:
      - say: No microphone, no editing.
      - type: TextEdit Done.
  - wait: 1s
  - record-stop
```

## Quoting

YAML does the quoting, so the same file works on macOS, Windows cmd and
PowerShell. Inline, wrap the YAML in single quotes on zsh/bash and double
quotes on cmd. Inside a step, `"two words"` stays one word; a comma inside
inline (flow) YAML needs the whole value quoted: `[type: "Notepad a, b"]`.
