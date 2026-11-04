# aos batch — spec (agreed with owner 2026-11-04)

YAML only. Steps run top to bottom. The only flow construct is `parallel`.

```yaml
subtitle: {position: bottom-center, size: L}   # optional: caption style for every say step
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
Short names: `click` (mouse-click --find), `type` (key-type), `key` (key-press), `screenshot`, `say`, `wait` (a duration).
`say:` speaks the text and shows it as a subtitle while it is spoken (cleared when it ends; `--no-subtitle` turns that off). It takes every `subtitle show` style flag (`--position`, `--size`, `--color`, ...); long text is captioned a sentence or clause at a time, in step with the voice.
A top-level `subtitle:` map sets the default caption style for every `say:` step (`subtitle: {position: bottom-center, size: L}`); a step's own flags override it.
Long form allowed: `click: { app: Calculator, target: Seven }`.
`do: <any aos command>` is the escape hatch.
System-level commands (power, system, pkg, service, network) are not verbs. Through `do:` the check warns.

## Commands
- `aos batch <file | inline yaml>`: run it
- `aos batch --check <file>`: validate only

## Not in v1
repeat, wait-until, manual, if, loops, variables. Add them only when there is a real need.
