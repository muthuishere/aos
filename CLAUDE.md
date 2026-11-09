<!-- ctx-optimize:begin -->
<ctx-optimize>
  <precondition>Run `command -v ctx-optimize` first. If it is NOT installed, IGNORE this entire
  block and answer by reading the code normally — the store is an optimization, not a requirement
  (install later with `npm install -g @muthuishere/ctx-optimize`, or download the binary). Everything
  below applies ONLY when the command exists.</precondition>
  <store>MULTI-MODULE repo, pre-built knowledge store at `~/ctxoptimize/aos/` — one graph per module + a navigator, 2 modules declared in `.ctxoptimize/config.json`.</store>
  <use>Use it INSTEAD of grep-and-read chains — PICK BY INTENT: find → `ctx-optimize query "<terms>"` ·
  inspect a symbol → `card <symbol>` · about to EDIT → `change-plan <symbol>` (callers+impact+tests, one
  call) · blast radius → `affected <symbol>` · connection → `path <a> <b>` ·
  list/filter (no jq): `nodes --kind K` / `edges --relation R` / `deps --scope dev`.
  Scope follows your cwd: a module dir answers from that module (zero hits escalate repo-wide); the root
  federates via the navigator (`~/ctxoptimize/aos/navigator.md`; `--modules all|a,b` widens).
  Output is parsed fact with exact file:line — cite it directly, do NOT re-verify in source.
  Exhaustive literal-string sweeps stay grep's job.</use>
  <deep-doc>The FULL usage card — verify discipline, store-vs-grep ladder, sources (databases/
  buckets/queues/APIs by env-var name), remote push/pull, `up` — is committed at
  `.ctxoptimize/instructions.md`. Read it before deeper store work.</deep-doc>
  <no-local-store>Fresh clone with nothing at `~/ctxoptimize/aos/`? Run `ctx-optimize up` —
  it pulls the team's prebuilt store when the config declares one, otherwise rebuilds every module store in seconds.</no-local-store>
</ctx-optimize>
<!-- ctx-optimize:end -->

## Windows: no PowerShell

No `powershell`, `pwsh` or `.ps1` anywhere in aos: not in Go code, tests,
test helpers or scripts, not even a temp `.ps1` run with `-File`. Use native
Go (Win32 and COM through `syscall.SyscallN` / `golang.org/x/sys/windows`) or
a `.cmd` file. Why: Windows Defender quarantined `aos.exe` as potentially
unwanted software after it started a hidden PowerShell, and the owner forbids
it. Naming PowerShell as a terminal app to *detect* (e.g. in
`internal/engine/input_target.go`) is fine. Live Windows checks live in
`test/windows/` as Go programs with `.cmd` runners.

## Design checklist: where does it run?

Every ADR and design doc answers these (template: `docs/adr/TEMPLATE.md`):

1. **macOS and Windows.** How it works on both. If either is unsupported,
   the feature does not ship. Linux is optional.
2. **Host side.** `windows-host` (follows the screen: under WSL it acts on the
   Windows desktop through `aos.exe`, over SSH on the person's laptop) or
   `local` (runs inside WSL / on the server).
3. **From WSL.** How it is exercised from inside WSL.

Every new group gets a `hostTable` entry in `internal/cli/host.go`;
`TestEveryCommandIsClassifiedForTheHost` fails without one.
