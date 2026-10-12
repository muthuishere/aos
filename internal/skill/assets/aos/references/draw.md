# aos draw: point at things on the human's screen

When the human must do something themselves (click Allow, pick a menu item),
draw a big arrow with a sign instead of describing where to look. Click-through,
never steals focus, ends by itself, needs no admin rights and no macOS
permission. macOS and Windows; on Linux only `--preview` works.

```bash
aos draw --at=760,500 --text="Click Allow"                   # point; blocks 8 s
aos draw --rect=900,300,200,60 --style=box --text="This field"
aos draw --app="System Settings" --text="This window"        # boxes the window
aos draw --mouse --style=ring --text="You are here"
aos draw --at=400,300 --monitor=2 --text="..."               # relative to monitor 2
aos draw --at=X,Y --text="..." --detach --json               # returns at once (300 s)
aos draw stop                                                 # remove every detached drawing
aos draw --at=X,Y --text="..." --preview=/tmp/out.png --json # render only, show nothing
```

By text, no coordinates:

```bash
aos draw --app="System Settings" --find=Allow --role=button --text="Click Allow"
aos draw --app=Notepad --find=File --role=menuitem --style=ring --text="Menus"
```

`--find` uses the same search as `aos find` (`--source`, `--role`, `--exact`,
`--nth=N` for the n-th best). Exit 3 = not found, with the closest labels.

Exactly one target: `--at`, `--rect`, a window (`<app>`/`--app`/`--title`) or
`--mouse`. `--duration` is seconds (0 = until `draw stop`); default 8, or 300
with `--detach`. `--json` prints where it landed: `Tip`, `Sign`, `Monitor`,
`Surface`, `PixelScale`, and `Found` for `--find`.

## Look

`--style=arrow|ring|box` · `--shape=bend|straight|zigzag|spiral` · `--size=S|M|L` ·
`--color=red|orange|amber|yellow|green|teal|blue|purple|pink|black|white|#RRGGBB` ·
`--text-color` · `--border=shadow|white-black|black` · `--corners=round|sharp` ·
`--from=auto|top-left|top|top-right|left|right|bottom-left|bottom|bottom-right` ·
`--font=<name|path>` · `--no-config`.

zigzag = urgent; spiral = impossible to miss; green = "yes, this one". Several
`--detach` drawings make numbered steps ("1. ...", "2. ..."); each new sign
keeps clear of the live ones.

Defaults: `~/.config/aos/draw.json` (`$AOS_CONFIG` overrides). If that file is
absent, the legacy `$WINDOWCTL_CONFIG` / `~/.config/windowctl/fontconfig.json`
is read instead.
`aos draw config` shows it, `aos draw config --init` creates it,
`aos draw fonts` lists font names.

`--detach` returns at once (default 300 s, or `--duration`); `aos draw stop`
ends every detached drawing. Nothing else needs to be installed.

## Recipe: verified pointing

1. `aos capture screenshot --out=/tmp/s.png` → pick the pixel; global = origin + pixel.
   Or skip the pixels: `aos find --app=X --text=Allow --json`.
2. `aos draw --at=X,Y --text="..." --detach --json` → check `Tip` == X,Y.
3. Optionally screenshot again to see what the human sees.
4. When they have done it: `aos draw stop`.

## Rules

- Say what to do in the sign, in the human's words ("Click Allow so Terminal
  can control your Mac"), not just "here".
- Never draw over a permission or security prompt to steer the human toward a
  choice they did not ask for; explain the consequence instead.
- Clean up with `aos draw stop` once the step is done.
