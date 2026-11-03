# aos batch — spec (agreed with owner 2026-11-04)

YAML only. Steps run top to bottom. The only flow construct is `parallel`.

```yaml
steps:
  - launch: calc
  - parallel:
      - say: seven plus two
      - click: Calculator Seven
  - type: Notepad done
  - wait: 2s
```

One line = the same YAML inline:

```sh
aos batch '[launch: calc, parallel: [say: seven, click: Calculator Seven], type: Notepad done]'
```

## Rules
- A step is one verb. `parallel` runs its list together; the next step waits for all of them.
- A failure stops the run.
- The whole file is checked before step 1; nothing runs on an error.

## Verbs
Every aos command is a verb named `<group>-<command>`, with the same arguments and flags as the CLI.
Short names: `click` (mouse-click --find), `type` (key-type), `key` (key-press), `screenshot`, `record` / `record-stop`, `say`, `wait` (a duration).
`say:` speaks the text and shows it as a subtitle while it is spoken (cleared when it ends; `--no-subtitle` turns that off), so a recorded batch gets voice and captions in sync with no editing.
Long form allowed: `click: { app: Calculator, target: Seven }`.
`do: <any aos command>` is the escape hatch.
System-level commands (power, system, pkg, service, network) are not verbs. Through `do:` the check warns.

## Commands
- `aos batch <file | inline yaml>`: run it
- `aos batch --check <file>`: validate only

## Not in v1
repeat, wait-until, manual, if, loops, variables. Add them only when there is a real need.
