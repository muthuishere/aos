# internal/engine

The desktop engine behind aos: windows, monitors, zones, input, screenshots,
find (accessibility + OCR), draw (arrows, boxes, text, subtitles, animation)
and the LAN remote. Platform adapters live in `adapter/{darwin,linux,windows}`,
shared types in `core/`.

## Provenance

Absorbed from windowctl (github.com/muthuishere/windowctl, MIT License,
Copyright (c) 2026 Muthukumaran Navaneethakrishnan) at origin/main `c36983e`
plus its then-open PRs merged in order: #16 fix/chrome-ax-resolve (`f230998`),
#17 feat/draw-animate (`4bb39d2`), #18 feat/draw-text (`bf11f2f`). windowctl is
deprecated and frozen; all engine changes happen here.

Left behind on purpose: `cmd/windowctl`, the site, the npm package, the
deprecation / skill auto-install / skill-retire code and the windowctl skill
assets (aos ships its own skill).

## Config and environment

| what | primary | legacy fallback (read only when primary is absent) |
|---|---|---|
| draw defaults file | `$AOS_CONFIG`, else `~/.config/aos/draw.json` (`$XDG_CONFIG_HOME` honoured) | `$WINDOWCTL_CONFIG`, else `~/.config/windowctl/fontconfig.json` |
| detached-draw handshake | `AOS_DRAW_CHILD`, `AOS_DRAW_TAG` (internal, parent → child) | — |
| detached-draw registry | `<UserCacheDir>/aos/draw` | — |

To migrate, copy `~/.config/windowctl/fontconfig.json` to
`~/.config/aos/draw.json`; the format is unchanged.
