# aos tab: browser and terminal tabs

A browser or a terminal is just a window *kind*; `tab` reaches inside it.
macOS and Windows; Linux not yet (exit 2).

```bash
aos tab list                                   # every browser and terminal tab
aos tab list --kind=browser --json             # id, app, kind, window, index, title, url, cwd, active, source
aos tab list --app=Ghostty                     # one app (loose: chrome, ghostty, safari)
aos tab list --window=22626                    # one window (aos window id or the app's own)
aos tab focus --url=github.com/me/repo/pulls   # raise the window, select the tab
aos tab focus --title=Inbox --app=Chrome --exact
aos tab focus --cwd=os-cli --kind=terminal     # the terminal tab sitting in that folder
aos tab open https://example.com --app=Chrome  # prints the new tab's id
aos tab open https://example.com --app=Safari --new-window --background
aos tab open --app=Ghostty --cwd=~/src/aos --command="task check"
aos tab send --id=ghostty:tab-group-1:tab-2 --text="ls -la"   # no focus needed
aos tab send --id=ghostty:tab-group-1:tab-2 --key=enter        # or ctrl+c
aos tab close --id=chrome:1658109204:1658109210                 # one tab, never the window
```

Text output marks the selected tab of each window with `*` and prints panes
(terminal splits) indented under their tab.

## Ids

`<app>:<app window id>:<tab id or index>[:<pane id>]`, as `tab list` prints
them. Pass them back verbatim.

- Chrome-family, Ghostty: the app's own tab id (survives other tabs closing).
- Safari, iTerm2, Terminal.app: the tab's 1-based index (changes when an
  earlier tab closes; list again).
- Accessibility fallback: `ax:<aos window id>:<n>` (macOS), `uia:<hwnd>:<n>`
  (Windows).

`window` in JSON is the aos window id (`window list`) when it could be matched
by app, bounds and title; `native_window` is the app's own. On macOS each
native tab is its own window, so a Ghostty or Terminal window's aos id changes
when you switch tabs; the native id does not.

## Who answers (macOS)

| app | via | list | focus | open | send | close |
|---|---|---|---|---|---|---|
| Chrome, Edge, Brave, Vivaldi, Chromium, Canary | JXA | title, url | yes | url, `--new-window`, `--background` | no | yes |
| Safari | JXA | title, url | yes | url, `--new-window`, `--background` | no | yes |
| Ghostty 1.3+ | JXA | title, cwd, panes | tab and pane | `--cwd`, `--command` | `--text`, `--key` | yes |
| iTerm2 | JXA | title, cwd, panes | tab and pane | `--cwd`, `--command` | `--text`, enter, ctrl+letter | yes |
| Terminal.app | JXA | title | yes | new window only | `--text` (runs it as a command) | only a window's single, idle tab |
| anything else | accessibility (AXTab) | title | yes (press/click) | no | no | cmd+w, best-effort (below) |

Apps that are not running are skipped, never launched. Each provider gets
1.5 s; one that times out is reported on stderr and the rest still print.

`--command` starts the tab with `$SHELL -lc '<cmd>; exec $SHELL -l'`, so the
command runs once and the tab stays open with a normal shell after it.

## Windows

Every app is read through UI Automation (TabItem): Edge, Chrome, Windows
Terminal, Notepad. Titles only, no URLs (Edge's "- Memory usage - NN MB" suffix
is stripped). Focus selects the tab, close sends ctrl+w after verifying the
window is in front. `open` uses the app's own command line (`msedge`/`chrome`
`--new-window URL`, `wt -w 0 new-tab -d DIR`). `send` is refused.
No URL or folder passes through `cmd.exe`: the browser exe is found in App
Paths and gets the URL as one argument after `--`; `wt.exe` gets argv with
`;` escaped. `--command` for Windows Terminal is a shell command by design
(`cmd /k <command>`). Close uses ctrl+w in browsers and Notepad,
ctrl+shift+w in Windows Terminal, and refuses apps it does not know.

## The page inside a browser tab: read, type, click, watch

```bash
aos tab read --id=ID                 # markdown; [e7] Message (textarea, empty)  [e8] Send (button)
aos tab read --id=ID --json          # + elements: ref, role, label, value
aos tab type --id=ID --element=e7 --text="hi" --enter
aos tab type --id=ID --into=Message --text="hi"
aos tab click --id=ID --element=e8
aos tab click --id=ID --find=Send --role=button
aos watch tab --id=ID --match="Build finished" --timeout=5m   # exit 0 when it appears, 3 on timeout
```

- Read first, then act by ref. A ref is an identity (role + label + HTML id
  + occurrence, remembered per tab and URL), not a position, so new chat
  messages above the box do not break it. Exit 3 when the element is gone or
  is now ambiguous. Read again.
- `--into` / `--find` match label, placeholder, title or description (exact
  first, then substring). Several matches: exit 4 with the candidates. Use
  `--element`, `--exact` or `--nth`.
- The frontmost app does not change. Click is an accessibility press. Type
  focuses the field through accessibility and posts keys to the browser
  process, so React or Lexical editors see real input.
- Type checks itself. The focused element must be the field (else exit 3,
  nothing typed). The same element (identity, not label) and the front window are re-checked every 16 characters and before Enter, and
  on a change typing stops and reports how many characters went out. The tab
  must still be selected with the same title and URL.
- Type makes the tab's window the browser's key window. If the browser is in
  front and the user is in another of its windows, it refuses unless you pass
  `--allow-focus`.
- A newline in `--text` is Return (it can send a chat message early). It is
  refused unless you pass `--allow-newline`. Use `--enter` at the end.
- Windows click with no Invoke pattern needs `--allow-focus` (it raises the
  window and clicks with the mouse).
- The tab is selected in its window if needed, without raising the window.
- `watch tab` prints `{"event":"tab","tab":ID,"added":[...]}` for new lines
  only. Text present at start is the baseline. Reordering, live clocks and
  field contents are not news. Blocks: use `--match` + `--timeout` or `--max`.
  Interval is 1 s by default and at minimum. Navigating the tab reports the new
  page as added text. If the tab closes, it exits 3.
- Chromium: aos turns on `AXEnhancedUserInterface` only when the page is
  missing, and turns it off again when the command ends.
- macOS: Chrome family and Safari. Windows: Edge/Chrome through UI Automation
  (type is not live-tested yet). Linux: not supported yet (exit 2).

## Accessibility close is best-effort

It selects the tab, re-checks that the tab's window is frontmost right
before pressing the shortcut (no key is sent otherwise), and reports success
only when the window's tab count dropped. A window that jumps in front in
between can still receive the key.

## Errors

- Exit 3: no tab matched (`focus`, `close`, `send`). List again. For the
  page commands: no element matched, or the page changed under a ref.
- Exit 4: `close` / `send` matched several tabs, or `tab type` / `tab click`
  matched several elements; the message lists them. Use
  `--id` or `--exact`. (`focus` takes the selected match, else the first.)
- Stale ids: index ids (Safari, iTerm2, Terminal) shift when an earlier tab
  closes, and Ghostty reuses tab ids. `--expect-title=<substring>` on
  `close` / `send` refuses (exit 3) unless the tab there still has that title.
- `macOS blocked aos from controlling <App> (Automation permission)`: the first
  call to each app shows a macOS prompt; if it was denied, turn it on in System
  Settings > Privacy & Security > Automation, or `tccutil reset AppleEvents`.
- `<App> tabs cannot receive input`: browsers have no input channel. Focus the
  tab and use `aos key type` / `aos key press`.
