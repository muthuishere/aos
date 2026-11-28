# ADR 0003: A public `ocr` Go package, cgo-free

- Status: **Accepted** (owner, through ctx-optimize, 2026-11-28)
- Date: 2026-11-28

## Context

ctx-optimize wants the OS's own text recognition as a Go library. It builds
with `CGO_ENABLED=0` and cross-compiles with goreleaser, so it cannot link
aos's darwin adapter, which reaches Vision through cgo. Shelling out is not
an option either: `osascript` and PowerShell can be blocked, and Windows
Defender already quarantined `aos.exe` once for starting a hidden
PowerShell. aos already has the engine work: Vision on macOS, a pure-Go
WinRT path on Windows.

## Decision

Add `github.com/muthuishere/aos/ocr`, a public package with no cgo:

```go
func RecognizeFile(path string, opts Options) ([]Line, error)
func RecognizeImage(img image.Image, opts Options) ([]Line, error)
type Line struct { Text string; X, Y, W, H float64; Confidence float64; Words []Word }
type Word struct { Text string; X, Y, W, H float64 }
type Options struct { Languages []string; Fast bool }
var ErrUnsupported error
```

Input is an image file or an `image.Image`, never a screen capture, so it
needs no permission. Boxes are image pixels from the top left; lines are
sorted into reading order (rows top to bottom, left to right in a row).
The zero `Options` match aos's own settings. No new command: aos's screen
OCR keeps its current code.

## Platforms and host (required)

1. **macOS:** Vision `VNRecognizeTextRequest` (accurate level, language
   correction) through the Objective-C runtime with
   `github.com/ebitengine/purego` (`objc_msgSend`, Vision.framework). The
   image is handed over as PNG data (`initWithData:options:`). Works on
   arm64 and amd64 (struct returns use `objc_msgSend_stret` there).
   **Windows:** WinRT `Windows.Media.Ocr` over COM vtables with
   `syscall`, the same steps as aos's screen OCR, no PowerShell. Needs an
   OCR language for the user (in-box for the display language). Windows
   reports no confidence, so `Confidence` is 0 there.
   **Linux:** `ErrUnsupported`; no engine is bundled (no tesseract).
2. **Host side:** none. It is a library, not a command, so `hostTable` is
   unchanged; it runs in the calling process.
3. **From WSL:** a Linux build returns `ErrUnsupported`. A WSL tool that
   needs OCR runs its Windows build, or `aos find` on the host.

## Consequences

- A public API: changes to it follow semver from this release on.
- purego becomes a dependency of aos (small, pure Go).
- The WinRT OCR steps now exist twice (adapter and package). Moving the
  adapter onto the package is a follow-up once the package has run on more
  Windows machines.

## Alternatives considered

- **Shell out to osascript / PowerShell:** blocked on locked-down machines,
  and PowerShell got aos flagged by Defender.
- **cgo with a build-tag fallback:** breaks `CGO_ENABLED=0` cross builds,
  which is the whole point.
- **tesseract or another bundled engine:** heavy, and worse than the OS
  engines on screen text.
