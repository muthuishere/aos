# Automating the UI: the visual loop

screenshot → read the pixel → click / type → screenshot again.

```bash
aos capture screenshot --app=Chrome --out=/tmp/c.png   # prints the captured rect WxH+X+Y
aos capture screenshot --monitor=2 --out=/tmp/m2.png
aos capture screenshot --region=100,100,800,600 --out=/tmp/r.png
aos mouse move 400 300 · aos mouse click 400 300 --double · aos mouse click --button=right
aos mouse position                                      # x y
aos key type "hello" --app=TextEdit --verify           # focus-free + read back: see below
aos key press enter --app=TextEdit                      # posted to the app, no focus change
aos mouse click --app=Calculator --find=7 --role=button # AXPress / UIA Invoke: no pointer, no focus
aos launch Terminal --wait                              # start an app and wait for its window
aos window wait Chrome --timeout=5000                   # ms
```

- **Point normalization:** every capture is resampled so 1 image pixel = 1
  global point (retina-safe). Global click point = capture origin + pixel.
- **Always name the target:** pass `--app`/`--title` to `key type` / `key press`.
  The person may be typing on another monitor while you work, so input to a
  named window never goes through OS focus: macOS posts the keys to that
  app's process (CGEventPostToPid, after making the window the app's key
  window without activating it); Windows posts WM_CHAR / WM_KEYDOWN to the
  window's focused text control. Their focus, keyboard and pointer are not
  touched. Without a target, keys go to whatever is frontmost — avoid.
- **`--verify`** reads the target's focused text field afterwards (AX value
  on macOS, WM_GETTEXT on Windows) and exits 1 unless the text arrived; it
  refuses to type at all when the window has no readable text field. Use it
  when the outcome matters — it is the check, not a screenshot.
- **`--focus`** forces the old path: focus the window, wait until it reports
  focused, type real keystrokes, then hand focus back. aos takes it on its
  own only where posting is known not to work, and says so (`focus (reason)`
  / `--json` `Route`/`Reason`):
  - macOS Chromium-family browsers (Chrome, Edge, Brave, Arc, Vivaldi,
    Opera) ignore posted keys; Electron apps may too — use `--verify`, and
    `--focus` if it fails.
  - Windows: UWP/WinUI apps (Calculator, apps hosted in
    ApplicationFrameHost) have no Win32 control to post to; chords with
    ctrl/alt/shift/win cannot be posted (the app reads modifiers from the real
    keyboard). Typing posts only to text controls (Edit, RichEdit, Notepad,
    Scintilla); other windows fail loud with a `--focus` hint.
  - Posted keys go to the app's key window: on macOS aos makes the matched
    window key inside its app first and refuses if it cannot.
- **`mouse click --app --find`** presses the found element through
  accessibility (AXPress / UI Automation Invoke): no pointer move, no focus.
  It searches the accessibility tree only (OCR would raise the window); add
  `--focus` to fall back to raising the window and clicking with the pointer.
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
