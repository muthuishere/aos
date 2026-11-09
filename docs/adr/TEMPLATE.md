# ADR NNNN: <decision, as a short sentence>

- Status: **Proposed**
- Date: YYYY-MM-DD

## Context

What problem, for whom, and what already exists to build on. Constraints
(aos stays small, agentless, no PowerShell on Windows).

## Decision

What we will do, concretely: commands, flags, output.

## Platforms and host (required)

1. **macOS:** how it works (APIs, permissions).
   **Windows:** how it works (Win32 / COM, no PowerShell).
   Both are required: if either is unsupported, the feature does not ship.
   **Linux:** optional; say what it does or that it is refused with a reason.
2. **Host side:** `windows-host` or `local`.
   - Under WSL: does it act on the Windows desktop (through `aos.exe`) or
     inside WSL?
   - Over SSH: on the person's laptop (forwarded socket) or on the server?
   - Add the group, or a full-route override, to `hostTable` in
     `internal/cli/host.go`.
3. **From WSL:** the command(s) that exercise it from inside WSL, and what
   the result looks like (paths translated with `wslpath`, etc.).

## Consequences

What gets easier, what gets harder, what we give up.

## Alternatives considered

Each with the reason it lost.
