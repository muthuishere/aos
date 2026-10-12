# macOS permissions

Two separate grants, both per parent process (the terminal or agent host that
runs aos, not aos itself):

| grant | needed by | check / request |
|---|---|---|
| Accessibility | window move/resize/focus/arrange, mouse, key, `find` (tree) | `aos permission check` · `aos permission request accessibility` |
| Screen Recording | `capture screenshot`, `find --source=ocr`, `remote share` | `aos permission request screen` |

- `permission check` never shows a dialog; `permission request` is the only
  thing that prompts. Other commands just fail with "permission denied".
- After granting in System Settings → Privacy & Security, quit and reopen the
  host app (Terminal, VS Code, the agent) so it picks up the grant, then re-run
  the original command.
- `draw` needs no permission.
- Blank screenshots or input that does nothing almost always mean a missing
  grant: `aos doctor` names the one.
- `move`/`focus` failing with "window … is gone from the AX tree": re-run with
  `WCTL_AX_DEBUG=1` and read the per-PID AX dump on stderr.
- Windows needs no grants; Linux (X11) needs `wmctrl`/`xdotool`, Wayland is
  best-effort.
