# Code mode: one program, many aos calls

`aos code` runs ONE small JavaScript program that calls many aos commands,
in parallel if you like, in a single process. Only the program's final value
comes back, so your context holds the answer, not every intermediate output.
Use it when a task needs several aos calls whose results feed each other
(find, then act, then verify), or many independent calls (measure 10 windows).
For a fixed list of steps with no logic, `aos batch` is simpler.

```bash
aos code operations                 # every operation (same names as `aos batch verbs`)
aos code describe window-list       # one operation: usage, input schema, output shape
aos code docs                       # the language reference
aos code check prog.js              # parse and validate; nothing runs (exit 2 on errors)
aos code run prog.js                # run it; prints the final value
aos code eval '<expression>' --json
aos code run - < prog.js
```

## Writing a program

Operations are `ops["<verb>"]`, the batch verb names: `window-list`,
`capture-screenshot`, `mouse-click`, and the short names `click`, `type`,
`key`, `screenshot`, `say`, `wait`. Input is `{args: [words], <flag>: value}`:
positional words go in `args`, every flag of the command is a key
(`true` -> `--k`, `false` -> `--k=false`, string/number -> `--k=v`).
Each call returns `{ok, exit, stdout, stderr, json?}`; `json` is stdout parsed
when it is JSON. A failing command does not throw: check `ok`.

```js
const [shot, wins] = await Promise.all([
  ops["capture-screenshot"]({ args: ["window"], app: "Calculator", out: "/tmp/c.png" }),
  ops["window-list"]({ json: true }),
])
wins.ok
  ? { shot: shot.stdout.trim(), apps: wins.json.map(w => w.App) }
  : { error: wins.stderr }
```

Do independent calls with `Promise.all`; do dependent ones with `await` in
order. The last expression is the result. The language is a small subset of
JavaScript (no `new`, no classes): `aos code docs` lists what it supports.

## What is not available

- No `exec` or `shell`: aos's own commands are the whole surface.
- System groups (`power`, `system`, `pkg`, `service`, `network`), `do`,
  `batch`, `serve`, `completion`, `skill`, `debug` are not operations.
- No say-first pacing: to speak, then act, `await ops.say({args: ["..."]})` first.
- The interpreter is not a sandbox for hostile code; operations run with aos's
  own privileges, same as typing the commands.

Run `aos code check` first when a program is long: a typo or an unknown
operation or flag fails before anything runs.
