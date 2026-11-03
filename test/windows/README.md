# Live Windows checks

Desktop-session checks for aos on a real Windows machine. They are Go programs
(native Win32, no PowerShell anywhere) with a `.cmd` runner each.

Build on any OS (arm64 shown; use amd64 for x64 boxes):

```bash
GOOS=windows GOARCH=arm64 go build -o out/ ./cmd/aos ./test/windows/...
```

Copy `out/*.exe` and `test/windows/*.cmd` to one folder on the Windows box and
run a `.cmd` there:

| runner | checks |
|---|---|
| `focusfree.cmd` | focus-free input into terminals, a Win32 form, Notepad, Calculator, Settings, Explorer, Edge; foreground and pointer never move |
| `edge.cmd` | `tab list/read/type/click` on a local page in an InPrivate Edge window behind another window |
| `wt-tabs.cmd [old-tag ...]` | Windows Terminal tabs in the background, Calculator's first press, a Paint drag spike |
| `hidden-desktop-spike.cmd` | running apps and aos on a hidden secondary desktop |

Each saves `shot-*.png` before/after every step and `shot-*results.json`
next to itself, and closes only what it opened. `testform.exe <title>` is the
classic Win32 window (an EDIT and a ClickMe button) the checks drive.
