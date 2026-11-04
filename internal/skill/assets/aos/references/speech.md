# Speech: `aos say`

Free, built-in voices only (macOS and Windows):

- **macOS** — the system `say` (all installed voices, e.g. Samantha).
- **Windows** — SAPI 5 over COM, called from Go (no PowerShell): the OneCore
  voices first (e.g. Microsoft David, Zira, Mark), then the desktop SAPI ones
  ("... Desktop"). Native Windows only; not under WSL.

## Commands

```bash
aos say "build finished"                              # speak AND show it as a subtitle, return when done
aos say "build finished" --no-subtitle                # speak only
aos say "Hello" --voice=Samantha --rate=190           # voice name (or part), words per minute
aos say "Saving now" --position=top-center --size=L --app=Notepad   # caption style: any subtitle show flag
aos say "Welcome" --out=~/Desktop/welcome.mp3         # file only: .mp3 .wav .aiff .m4a (mp3/m4a via ffmpeg)
aos say voices [--json]                               # Name, Language, Gender, Engine (say|onecore|sapi)
aos say "Now I open Notepad" -- aos key type --app=Notepad "hello"   # speak AND act, wait for both
aos say "..." --then-wait=false -- aos launch calc    # return when the command ends; speech keeps going
aos say "step one" --async                            # return at once, keep talking
aos say wait [--timeout=30]                           # block until every queued line has finished
aos say stop                                          # silence now, drop the queue
aos subtitle show "Saving the report" --say           # a caption that speaks (plain `subtitle show` stays silent)
```

## Speech and subtitle go together

- `say` shows the same text as an aos subtitle while it is heard, and clears
  it when the speech ends. `--no-subtitle` turns that off; a file-only
  `--out` shows nothing.
- **Style:** `say` takes every `subtitle show` style flag (`--position`,
  `--size`, `--color`, `--bg`, `--font`, `--weight`, `--align`, `--max-width`,
  `--margin`, `--offset`, `--app`/`--title` to caption over one window, ...),
  passed through unchanged. The default look is the subtitle default: white
  on a translucent dark pill, bottom-center.
- **Long text** is captioned in chunks: a sentence at a time; a sentence
  longer than about two lines at its clauses; a clause still too long at word
  boundaries. Very short pieces join the caption before them. Each chunk is
  rendered as its own clip, so its caption goes up as its audio starts and is
  replaced when the next chunk starts.
- `subtitle show` stays a silent caption; `subtitle show --say` also speaks it
  (with no second caption).

## Queueing and acting

- Lines **queue**: a second `say` (async or not) waits for the one speaking,
  so lines never talk over each other.
- With `-- <command>`, the exit code is the command's; a speech failure only
  warns on stderr. `aos ...` after `--` runs in the same process (no new
  startup, no shell quoting); anything else runs as a program.
- In `aos batch`, a `say:` step speaks and captions the same way; put it in
  `parallel:` with an action to talk while acting (`references/batch.md`).
