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

## Accessibility close is best-effort

It selects the tab, re-checks that the tab's window is frontmost right
before pressing the shortcut (no key is sent otherwise), and reports success
only when the window's tab count dropped. A window that jumps in front in
between can still receive the key.

## Errors

- Exit 3: no tab matched (`focus`, `close`, `send`). List again.
- Exit 4: `close` / `send` matched several tabs; the message lists them. Use
  `--id` or `--exact`. (`focus` takes the selected match, else the first.)
- Stale ids: index ids (Safari, iTerm2, Terminal) shift when an earlier tab
  closes, and Ghostty reuses tab ids. `--expect-title=<substring>` on
  `close` / `send` refuses (exit 3) unless the tab there still has that title.
- `macOS blocked aos from controlling <App> (Automation permission)`: the first
  call to each app shows a macOS prompt; if it was denied, turn it on in System
  Settings > Privacy & Security > Automation, or `tccutil reset AppleEvents`.
- `<App> tabs cannot receive input`: browsers have no input channel. Focus the
  tab and use `aos key type` / `aos key press`.
