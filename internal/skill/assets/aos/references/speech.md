# Speech: `aos say`

Free, built-in voices only (macOS and Windows):

- **macOS** — the system `say` (all installed voices, e.g. Samantha).
- **Windows** — WinRT `Windows.Media.SpeechSynthesis` first (it has the
  Natural / OneCore voices when installed), System.Speech (SAPI 5) as the
  fallback. Native Windows only; not under WSL.

Every line is rendered to an audio file, then played, so the same audio can
be dropped into a recording.

## Commands

```bash
aos say "build finished"                              # speak, return when done
aos say "Hello" --voice=Samantha --rate=190           # voice name (or part), words per minute
aos say "Welcome" --out=~/Desktop/welcome.mp3         # file only: .mp3 .wav .aiff .m4a (mp3/m4a via ffmpeg)
aos say voices [--json]                               # Name, Language, Gender, Engine (say|winrt|sapi)
aos say "Now I open Notepad" -- aos key type --app=Notepad "hello"   # speak AND act, wait for both
aos say "..." --then-wait=false -- aos launch calc    # return when the command ends; speech keeps going
aos say "step one" --async                            # return at once, keep talking
aos say wait [--timeout=30]                           # block until every queued line has finished
aos say stop                                          # silence now, drop the queue
aos subtitle show "Saving the report" --speak         # caption and say it
```

- Lines **queue**: a second `say` (async or not) waits for the one speaking,
  so narration never talks over itself.
- With `-- <command>`, the exit code is the command's; a speech failure only
  warns on stderr. `aos ...` after `--` runs in the same process (no new
  startup, no shell quoting); anything else runs as a program.

## Narrating a recording

While `aos capture video start ...` runs, each `say` is time-stamped (event
kind `say` in `<out>.events.json`) and kept. `aos capture video stop` mixes
every line into `<out>.final.mp4` at the moment it was spoken:

| stop flag | result |
|---|---|
| `--narration=mix` (default) | voice over the recorded system audio (if any) |
| `--narration=replace` | voice only |
| `--narration=off` | no voice track added |

`say --narrate "..."` adds a line to the track **without** playing it aloud
(useful on a muted machine); it still holds the queue for the line's length,
so the next line does not start inside it.

```bash
aos capture video start app --app=TextEdit --out=~/Desktop/demo.mp4
aos say "Here is TextEdit" --async
aos say "I type a note while I talk" -- aos key type --app=TextEdit "Hello from aos"
aos say wait
aos capture video stop      # -> demo.final.mp4 with the voice in sync
```
