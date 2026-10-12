# aos find: locate a button, menu, link or phrase by its text

Returns global coordinates (the same space as `capture`, `mouse` and `draw`), so
the result can be clicked or pointed at directly. macOS and Windows; Linux not
yet.

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
