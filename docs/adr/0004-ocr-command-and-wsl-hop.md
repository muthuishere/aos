# ADR 0004: `aos ocr`, and Windows OCR from WSL through aos.exe

- Status: **Accepted** (owner, through ctx-optimize, 2026-11-28)
- Date: 2026-11-28

## Context

ADR 0003 added the cgo-free `ocr` package. Its Linux build returns
`ErrUnsupported`, but many ctx-optimize users run inside WSL, where Windows
OCR is one process away. aos already reaches Windows from WSL for screen
commands: it finds `aos.exe` and translates paths with `wslpath`.

## Decision

1. A command, `aos ocr <image|-> [--lang=a,b] [--fast] [--json]`: reads
   a PNG, JPEG or GIF file (or stdin) through the `ocr` package. Text
   output is one line per line; `--json` is the package's `[]Line`
   (`text, x, y, w, h, confidence, words`).
2. The `ocr` package, built for Linux and running inside WSL, hands the
   work to Windows: it finds `aos.exe`, converts the path with
   `wslpath -w`, runs `aos.exe ocr <path> --json` and parses the result.
   `RecognizeImage` writes a temporary PNG first. `Options.NoWSLHop`
   turns the hop off. When `aos.exe` is missing, interop is off or
   `aos.exe` predates `aos ocr`, the error wraps `ErrUnsupported` and
   says how to fix it. `AOS_WINDOWS_EXE` names `aos.exe` directly.
3. WSL detection, the `aos.exe` lookup and path translation move from
   `internal/cli` into `internal/wslhost`, used by both. Behaviour of the
   CLI is unchanged.

## Platforms and host (required)

1. **macOS:** Vision, as in ADR 0003. **Windows:** WinRT
   `Windows.Media.Ocr`, as in ADR 0003. **Linux:** inside WSL, Windows OCR
   through `aos.exe`; elsewhere `ErrUnsupported`.
2. **Host side:** `local` (`"ocr": false` in `hostTable`). The file is
   on the machine aos runs on; under WSL the package itself does the hop,
   so the CLI never sends the command across. Over SSH it runs on the
   server (a Linux server returns the unsupported error).
3. **From WSL:** `aos ocr ~/shot.png` prints the text, read by the
   Windows OCR; the path reaches `aos.exe` as
   `\\wsl.localhost\<distro>\home\...`. A Go program built for Linux
   gets the same through `ocr.RecognizeFile`.

## Consequences

- WSL users get real OCR with no extra install beyond the `aos.exe` that
  install.sh already puts on Windows.
- One process start per call under WSL.
- `aos.exe` must be v0.20.0 or later; older ones give a clear "update"
  error.

## Alternatives considered

- **A Windows helper embedded in the library:** megabytes in every consumer
  binary, and an unsigned exe dropped into %TEMP% and run, the pattern
  Defender flagged for aos.
- **Leave the hop to each consumer:** every consumer would reimplement WSL
  detection, the aos.exe lookup and path translation.
- **Route `aos ocr` as a windows-host command:** works for the CLI, but
  not for a Go program using the package, so the package does the hop.
