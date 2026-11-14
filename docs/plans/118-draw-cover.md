# Plan: #118 blocking covers and private-data auto-cover

Issue: https://github.com/muthuishere/aos/issues/118 (body + three owner comments).

## Shape

A **cover** is a `draw` overlay that is filled, not annotated: an opaque box
(any colour, optional text, optional border) over a rect, a window, part of a
window, or text found on screen. It reuses the draw stack:

- the overlay window comes from the darwin adapter (`wctl_overlay_show`), with
  a new `core.CoverOverlayer` capability so a cover can move/resize, hide and
  optionally swallow input;
- the text on it reuses `layoutText` / `loadTextFont` from `draw text`, with
  auto-fit (shrink until the text fits the box, never overflow);
- detached covers reuse the draw registry (`SpawnDetachedDrawTagged`, tag
  `cover`), so `aos draw stop` also removes them;
- presets live in the existing draw config file (`presets:`).

Design rule: **fail closed.** When in doubt the area stays covered or the
capture is refused. A cover with alpha < 1 is a *tint*, never redaction.

### Window follow (P1)

`--app/--title` pins the cover to the window ID resolved once. A ~30 Hz loop
re-reads the window list (`ListWindows` is z-ordered, layer-0, on-screen only):

- moved/resized → the cover moves/scales; `--rect` is window-relative, in
  points or `N%`;
- window missing from the on-screen list (minimised, hidden, other Space,
  closed) → the cover hides; nothing of the window is on screen to expose;
- the window list cannot be read → the cover stays where it is (fail closed);
- stacking: the cover stays at overlay level (always above the window, so it
  never falls behind when the window is raised) and **clips out** the parts
  of any window that is in front of the target. A frame of over-covering a
  front window is harmless; a frame of under-covering the target is not, so
  this is the safe direction.

Known limit: a poll-based follow can lag a fast drag by one tick. P2's pixel
redaction is the guarantee for files; P4 adds frame blanking for record/share.

## Phases

### P1 — `aos draw cover` on macOS
- Targets: `--rect=x,y,w,h` (global, or monitor-relative with `--monitor`),
  `--app/--title` (whole window, or `--rect` window-relative, `N%` allowed),
  `--find=<text>` in a window (`--role --nth --pad`).
- Look: `--color=<name|#rgb|#rrggbb|#rrggbbaa>` (default opaque black),
  `--text` (`\n` lines), `--text-color`, `--font --font-scale --size --weight
  --align --position --margin`, `--border=<px> --border-color --radius`,
  `--preset=<name>` from the draw config.
- Behaviour: `--follow` (default on for window targets), `--block-input`
  (default click-through), `--id`, `--duration`, `--detach`, `--preview`,
  `--json`.
- `aos draw cover list` (grouped by window), `aos draw cover stop [--id]`,
  `aos draw cover update --id=<n> --text/--color/...` (swap in one frame).
- Tests: render pixels (opaque fill, text inside the box, auto-fit), tint
  detection, window-relative and `%` rects, follow loop against a fake
  adapter (move → frame updates, minimise → hide, list error → stay,
  occluder → clipped), update without a gap.

### P2 — `aos screenshot --cover` pixel redaction, fail closed
- `--cover=<x,y,w,h>` (repeatable, global), `--cover-app/--cover-title`,
  `--cover-find=<text>`; live detached covers are also burned into the file.
- Capture to a private temp file, paint the covers into the pixels, verify
  every covered pixel reads back as the cover, then atomically rename into
  place. Any failure (target missing, tint, permission, verify) → exit
  non-zero, no file written.
- Tests read the PNG back and check every covered pixel.

### P3 — privacy mode on by default
- `~/.config/aos/privacy.yaml`: `apps`, `paths`, `patterns`, `allow`, `style`.
- Every `screenshot` runs auto-cover: private apps/windows, OCR pattern scan
  (Luhn cards, IBAN, SSN, PAN/Aadhaar, `sk-`/`ghp_`/`AKIA`/JWT, `-----BEGIN`),
  optional `sec` hash match, notification banners.
- Scan cannot run → refuse. `--no-privacy=<reason>` required to opt out,
  reason logged. `aos privacy status`, `aos privacy test --preview`.

### P4 — record and LAN screen-share
- Covers and auto-cover applied per frame; a frame whose scan is stale is
  blacked out; notification banners covered during record/share.

### P5 — Windows and Linux
- Windows: layered `WS_EX_TRANSPARENT` window (dropped for `--block-input`),
  `SetWindowPos` follow, Win32 only (no PowerShell). Linux follows the
  existing draw backend.

## Where it runs (design checklist)

1. macOS in P1–P4; Windows in P5 (the feature is not released as done until
   P5 lands, per the mac+windows rule).
2. Host side: `windows-host` — covers act on the screen the person sees.
3. From WSL: `aos.exe draw cover ...` through the existing host forwarding.

## Out of scope for this PR

P3–P5, `--gradient/--image`, blur/pixelate styles, restoring covers after the
drawing process is killed.
