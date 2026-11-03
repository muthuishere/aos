# aos find: locate a button, menu, link or phrase by its text

Returns global coordinates (the same space as `capture`, `mouse` and `draw`), so
the result can be clicked or pointed at directly. macOS and Windows; on Linux
through AT-SPI and tesseract (see `window text` below).

```bash
aos find --app=TextEdit --text=Format                       # best match, one line
aos find --app=TextEdit --text=Format --json                # {Text, Label, Role, Source, Bounds, Center}
aos find --app=Notepad --text=File --role=menuitem --json   # only menu items
aos find --title=Settings --text=Allow --exact --json       # whole label must equal it
aos find --app=Safari --text="Sign in" --all --json         # every match, best first (array)
aos find --app=Preview --text=Total --source=ocr --json     # pixels only (PDFs, images, canvas)
```

Text output: `centerX centerY  WxH+X+Y  source role "label"`.

## How it searches (`--source=auto`, the default)

1. **Accessibility tree** (macOS AX incl. the app's menu bar, Windows UI
   Automation): exact bounds from the app, even when partly covered.
2. **OCR** (macOS Vision, Windows.Media.Ocr, on-device) when the tree has no
   match: canvas apps, games, remote desktops, images, poor Electron apps. The
   window is raised first so it is visible; `--no-raise` skips that.
3. A partial hit in a big element (text area, web view) is narrowed with OCR of
   just that element (`Source: accessibility+ocr`).

macOS OCR needs Screen Recording (`aos permission request screen`); the tree
needs Accessibility.

Ranking: exact label > whole word > prefix > substring; interactive roles
(button, menuitem, link, tab, checkbox, radio) > textfield > text; then top to
bottom, left to right. `--role` is one of: button, menuitem, link, tab,
checkbox, radio, textfield, text, image.

## Exit codes

- **0** found · **3** the window or the text is not there · **1** other failure
  (permission denied, bad flag value) · **2** refused (no display).
- Exit 3 lists up to 8 of the closest labels it did see. Retry with one of
  those spellings; never guess coordinates instead.

## Rules

- Always name the window; the first match is searched.
- Prefer `--role` for controls ("File" the menu, not "File" in a sentence).
- Items inside a closed menu are not on screen: click the top-level item, then
  find again.
- Click it: `aos mouse click <Center.X> <Center.Y>`. Point at it:
  `aos draw --app=<App> --find="<text>" --text="..."`.

## aos window text: read ALL the text of a window

`find` locates one string; `window text` prints every piece of text in the
window, in reading order (rows top to bottom, each row left to right), with the
same sources: the accessibility tree first, OCR as the fallback.

```bash
aos window text TextEdit                         # plain text, one element per line
aos window text --focused --max-chars=4000       # the frontmost window, capped
aos window text --id=12345 --json                # {window, app, source, text, truncated, items[]}
aos window text --title=Inbox --source=ocr       # pixels only (raises the window first)
aos window text Ghostty --no-raise --timeout=1500
```

- Contents, not just names: text fields and editors give their value
  (macOS `AXValue`, Windows UIA `TextPattern` / `ValuePattern`, Linux AT-SPI
  `Text`), so editors, terminals and browser pages return their body. A
  labelled field prints as `label: value`. Password fields are never read.
- `--json` items: `{text, value, role, source, bounds}`; top-level `source` is
  `ax` (macOS), `uia` (Windows), `atspi` (Linux) or `ocr`.
- `--source=auto` (default) falls back to OCR when the tree holds almost no text
  (canvas apps, games, remote desktops) and for a Chromium window whose page is
  not exposed to accessibility. OCR raises the window unless `--no-raise`.
- Duplicates (a link and the label inside it) are merged; the window's own
  title-bar buttons and the menu bar are left out.
- `--timeout` (ms, default 5000) bounds the whole read; exit 3 = no such window.
- Linux: AT-SPI needs `at-spi2-core` (+ `libatk-adaptor` for GTK3); OCR needs
  `tesseract`. On Wayland apps report no screen coordinates, so boxes and order
  are best-effort; `aos doctor` reports all three.
