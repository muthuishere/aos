# Windows, monitors and layouts

All of this used to be the `window-ctl-skill` (windowctl). aos runs the same
engine as a library, so the behaviour is identical.

## Discover before acting

```bash
aos window list --json            # every app window: App, Title, Bounds, Monitor, Focused
aos window list --app=Chrome      # bare word = app name, falls back to the title
aos display list                  # monitors: id, geometry, ACTIVE (pointer) / FOCUSED (frontmost window)
aos display active                # id of the monitor the pointer is on
aos display focused               # id of the monitor holding the frontmost window
```

- `--title` is a case-insensitive substring; `--app` is a case-insensitive exact
  name. When several windows match, the **first** one is used — list first and
  ask the human when it is ambiguous.
- Monitors are **1-indexed by ascending (X, Y) origin**; the leftmost is `1`.
  Map "the laptop", "the external", "this screen" to an id from `display list`;
  never hardcode. A window's `Monitor` uses the same numbering (0 = off-screen).
- "This window" / "my current screen": read `Focused` from `window list --json`
  and FOCUSED from `display list` instead of guessing.

## Zones

| zone | where | | split `N:M` | where |
|---|---|---|---|---|
| `1A` | left half | | `2:1` / `2:2` | left / right half |
| `1B` | right half | | `3:1` `3:2` `3:3` | thirds |
| `2A` | top-left quarter | | `4:3` | third column of four |
| `2B` | top-right quarter | | | |
| `2C` | bottom-left quarter | | | |
| `2D` | bottom-right quarter | | | |

Zones are case-insensitive. Splits are vertical columns only (full height);
there is no thirds-tall or percentage grammar — use `--at` for anything else.
`aos window zones [--monitor=N]` prints the real rects; `--show --app=X` previews.

## Move, resize, focus

```bash
aos window move Chrome --zone=1A                    # left half of its current monitor
aos window move Slack --monitor=2 --zone=1:3        # first third of monitor 2
aos window move Chrome --at=0,25,1920,1055          # absolute global x,y,w,h
aos window move Chrome --monitor=2 --at=0,0,960,1080  # RELATIVE to monitor 2's origin
aos window resize Chrome --w=900 --h=700            # in place
aos window focus Chrome                             # raise + activate
```

- Without `--monitor` the window stays on the monitor it mostly overlaps.
- `--monitor` switches `--at` from absolute to monitor-relative. Getting this
  wrong puts the window off-screen.
- An OS/app clamp (minimum size, fullscreen, fixed-size windows like
  Calculator) fails with the requested vs. actual size. The window did move;
  report the message and do not auto-retry. Re-read `window list` for the truth.
- Stop after each action; do not chain unasked follow-ups.

## Many windows at once: layouts

```bash
aos window save ~/.config/aos/layouts/work.json     # capture the current arrangement
aos window arrange ~/.config/aos/layouts/work.json  # put it back
echo '[{"app":"Google Chrome","zone":"1A"},{"app":"Slack","zone":"1B"},
       {"app":"Terminal","monitor":2,"zone":"2D"}]' | aos window arrange -
```

Entries use **lowercase** keys: `app` or `title`, then `zone` or `x`/`y`/`w`/`h`,
optional `monitor`. Every entry is applied even when one fails; the output is one
`ok`/`fail` line per entry and the exit is 1 if any failed. Prefer one `arrange`
over several `move` calls when the human names two or more placements.

## Recipes

| ask | run |
|---|---|
| split X and Y 50/50 | arrange `[{"app":X,"zone":"1A"},{"app":Y,"zone":"1B"}]` |
| IDE layout (browser / editor / terminal) | arrange with zones `3:1`, `3:2`, `3:3` |
| send this window to the other monitor | `display list` → `window move <focused app> --monitor=<other> --zone=<same>` |
| four apps in quarters | arrange with `2A` `2B` `2C` `2D` |
