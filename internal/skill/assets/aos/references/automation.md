# Automating the UI: the visual loop

screenshot → read the pixel → click / type → screenshot again.

```bash
aos capture screenshot --app=Chrome --out=/tmp/c.png   # prints the captured rect WxH+X+Y
aos capture screenshot --monitor=2 --out=/tmp/m2.png
aos capture screenshot --region=100,100,800,600 --out=/tmp/r.png
aos mouse move 400 300 · aos mouse click 400 300 --double · aos mouse click --button=right
aos mouse position                                      # x y
aos key type "hello" --app=Chrome                       # focus guard: see below
aos key press cmd+shift+s --app=Chrome
aos launch Terminal --wait                              # start an app and wait for its window
aos window wait Chrome --timeout=5000                   # ms
```

- **Point normalization:** every capture is resampled so 1 image pixel = 1
  global point (retina-safe). Global click point = capture origin + pixel.
- **Focus guard:** always pass `--app`/`--title` to `key type` / `key press`.
  aos focuses the window and waits until it reports focused before sending a
  single key, so keystrokes cannot land in whatever had focus before (a real
  data-corruption risk right after `launch`).
- Prefer `aos find` over reading pixels when the target has a name (a button,
  a menu, a phrase): it returns exact bounds and a Center to click.

## Open a terminal and run a command in it

```bash
# macOS (Terminal; Ghostty / iTerm / "Visual Studio Code" work the same by app name)
aos launch Terminal --wait
aos key press cmd+n --app=Terminal        # optional: a fresh window (launch reuses a running app)
aos key type 'npm test' --app=Terminal
aos key press enter --app=Terminal
# Windows
aos launch WindowsTerminal --wait
aos key type 'dir' --app=WindowsTerminal
aos key press enter --app=WindowsTerminal
```

Check it ran: `aos find --app=Terminal --text="<expected word>" --source=ocr`
(exit 0 = on screen) or `aos capture screenshot --app=Terminal`.
If you only need the output, not a visible terminal, use `aos exec capture -- <cmd>`.

## Sharing the screen

`aos remote share [--monitor=N]` streams the screen and forwards mouse/keyboard
on the **LAN** only and needs no confirmation. aos has no public tunnel. If the
human wants a public link (`windowctl remote --tunnel`, a `*.trycloudflare.com`
URL anyone with the link can drive), say plainly that it puts desktop control on
the public internet and get an explicit "yes" first, every time.
