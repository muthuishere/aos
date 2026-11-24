# Code mode: one program, many aos calls

`aos code` runs ONE small JavaScript program that calls many aos commands,
in parallel if you like, in a single process. Only the program's final value
comes back, so your context holds the answer, not every intermediate output.

**Use one program instead of many aos calls when** a task needs several aos
calls whose results feed each other (find, then act, then verify), many
independent reads (measure 10 windows, read 5 tabs), a loop, or shaping data
before it reaches you. For a fixed list of steps with no logic, `aos batch` is
simpler; for a single command, just run it.

```bash
aos code operations                 # every operation (same names as `aos batch verbs`)
aos code describe window-list       # one operation: usage and input schema
aos code docs                       # the language reference
aos code check prog.js              # parse and validate; nothing runs (exit 2 on errors)
aos code run prog.js --json         # {ok, value, error, diagnostics, ms}
aos code run - --input '{"app":"Notepad"}' --json < prog.js   # data in as the global `input`
aos code eval '<expression>' --json
```

Exit codes: 0 ok, 1 the program ran and failed, 2 errors (nothing ran).

## Modules first

aos ships ready-made recipes as importable modules. Import one before composing
raw operations: they are tested, take plain values and return plain JSON.
`aos code modules` lists them, `aos code describe aos:<name>` shows the exports
and their docs.

| module | exports |
|---|---|
| `aos:layout` | `tile(apps, monitor)` 1-4 apps as full / halves / thirds / quarters; `sideBySide(left, right, monitor)` |
| `aos:ui` | `openApp(app, timeoutMs)` launch or focus, wait for the window, return it (or null); `waitForText(app, text, timeoutSec)` true/false; `clickThenExpect(app, text, expect, timeoutSec)` click a control by its text then confirm what appears |
| `aos:snapshot` | `snapshot(top)` windows, focused window, pointer, busiest processes in one parallel read; `shotEach(apps, dir)` one screenshot per app, in parallel |

```js
import { snapshot } from "aos:snapshot"
import { sideBySide } from "aos:layout"
await sideBySide("Code", "Safari", 2)
const s = await snapshot(3)
;({ windows: s.windows.length, focused: s.focused && s.focused.App, busiest: s.busiest.map(p => p.name) })
```

Only go to raw `ops["..."]` for what no module covers.

## Operations are JSON-first

Operations are `ops["<verb>"]`, the batch verb names (`window-list`,
`capture-screenshot`, `mouse-click`, plus the short names `click`, `type`,
`key`, `screenshot`, `say`, `wait`). Input is `{args: [words], <flag>: value}`:
positional words go in `args`, every flag of the command is a key
(`true` -> `--k`, `false` -> `--k=false`, string/number -> `--k=v`).

- If the command documents `--json`, aos adds it for you (set `json: false` to
  opt out).
- A call returns the parsed JSON value itself (array or object), or the
  trimmed stdout text when the command prints no JSON.
- A non-zero exit REJECTS with `<op>: exit N: <stderr tail>`: use try/catch, there is
  no `ok` to check.

```js
const [wins, shot] = await Promise.all([
  ops["window-list"]({}),                       // -> array of windows
  ops["capture-screenshot"]({ args: ["window"], app: input.app, out: "/tmp/c.png" }),
])
;({ apps: wins.map(w => w.App), shot })
```

```js
try { await ops["window-focus"]({ args: ["Notepad"] }); ({ focused: true }) }
catch (e) { ({ focused: false, why: e.message }) }
```

Do independent calls with `Promise.all`; dependent ones with `await` in order.
The last expression is the result. The language is a small subset of
JavaScript (no `new`, no classes): `aos code docs` lists what it supports.

## Inside Claude Code's Code Mode

If you already write code that makes shell calls, still send aos work as ONE
program, not one `aos` process per call:

```js
const r = await $`aos code run - --input ${JSON.stringify({ app: "Notepad" })} --json < prog.js`.json()
```

Read `r.value` (or `r.error` / `r.diagnostics`). Join results across tools in
your code; let each aos program do the work within aos.

## What is not available

- No `exec` or `shell`: aos's own commands are the whole surface.
- System groups (`power`, `system`, `pkg`, `service`, `network`), `do`,
  `batch`, `serve`, `completion`, `skill`, `debug` are not operations.
- No say-first pacing: to speak, then act, `await ops.say({args: ["..."]})` first.
- The interpreter is not a sandbox for hostile code; operations run with aos's
  own privileges, same as typing the commands.

Run `aos code check` first when a program is long or has side effects: a typo
or an unknown operation or flag fails before anything runs.
