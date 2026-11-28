# Characters and storytelling

`aos draw character` puts a cartoon person on screen: a click-through
drawing like `draw image`, recorded by `capture video`. Use characters to
tell a short story around a real app: a shopkeeper with a problem, a
programmer with the fix, a customer who is happy at the end.

## Pick a cast

```bash
aos draw character --list            # 25 presets, 21 roles, hair styles
aos draw character --list --json     # the same, for a program
```

A preset is a ready person (`priya` shopkeeper, `dev` customer, `arjun`
programmer, `meera` founder, `ravi` lawyer, `sana` doctor, `kabir` chef,
...). Or build one from a role template:

```bash
aos draw character --role=lawyer --gender=woman --skin=dark --hair-style=bun --glasses
aos draw character --role=shopkeeper --gender=man --outfit=#b5562b --moustache
aos draw character priya --outfit=#7a1f3d     # a preset with one thing changed
```

- Roles: casual, customer, developer (programmer, coder, engineer), founder
  (ceo, entrepreneur), office (manager), lawyer, doctor, nurse, scientist,
  chef (cook), shopkeeper (seller), teacher, student, police (officer),
  firefighter, pilot, mechanic, builder (construction), farmer, artist
  (painter), singer (musician).
- Looks: `--gender=man|woman`, `--skin=light|medium|tan|brown|dark|#hex`,
  `--hair=black|brown|auburn|blonde|grey|white|#hex`,
  `--hair-style=short|side|curly|long|bun|ponytail|bald`, `--beard`,
  `--moustache`, `--glasses`, `--outfit=#hex` (the role's main colour).
- Keep one look per person for the whole story, and give each person their
  side of the screen: one on the left, one on the right.

## Place and size

Default: bottom-left, 45% of the screen height. Everything `draw image`
takes works: `--position`, `--height` / `--width`, `--app` (inside a
window), `--monitor`, `--at`, `--margin`, `--duration`, `--detach`,
`--preview=out.png` (render without showing; never record the person's real
screen just to check a drawing).

```bash
aos draw character priya --position=bottom-left --height=40% --detach
aos draw character arjun --position=bottom-right --height=40% --detach
```

## Make them talk

Pair the character with a speech bubble pointing at them, and swap frames:
`--frame=talk` opens the mouth, `--frame=blink` closes the eyes,
`--frame=rest` is the default. Alternate rest and talk every ~0.3 s while
a line is on screen for a speaking look; blink every few seconds.

```bash
aos draw character priya --position=bottom-left --height=40% --frame=talk --duration=3
aos draw bubble "My stock sheet is a mess!" --at=380,620 --duration=3
```

Place the bubble beside the face (upper third of the character), never over
it. The same speaker keeps the same side for the whole story.

## A story in four beats

1. **Intro:** the two characters appear left and right; a caption names the
   problem.
2. **Problem:** the first character talks (bubble + talk frames).
3. **Solution:** the second character answers, then `draw` arrows and
   `--find` point at the real app doing the work.
4. **Outro:** both characters, a happy line, a caption with the call to
   action.

Record it with `capture video start smart` and stop at the end (see
recording-a-demo.md). Clean up detached drawings with `aos draw stop`.

## The SVG itself

`--save=priya.svg` writes the rigged SVG: groups `eyes-open` /
`eyes-closed`, `mouth-closed` / `mouth-open`, `arm-left` / `arm-right`,
colour groups named `<part>-<tone>` (`skin-base`, `shirt-base`,
`hair-base`, `coat-base`, ...), and anchors on the root (`data-speech`,
`data-face`, `data-chest`, `data-floor`). `draw image` drives any SVG
rigged this way:

```bash
aos draw image priya.svg --show=mouth-open --hide=mouth-closed
aos draw image priya.svg --recolor=shirt=#10b981,hair=#6b3e26
```
