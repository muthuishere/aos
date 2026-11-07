# Research: aos running ON Android and iOS

Date: 2026-11-07 · Status: research, nothing installed on any phone · Author: Claude (mobile worktree)

Owner ask: aos should be *installed on* the phone and run there like on macOS/Windows/Linux,
not only drive a phone from a Mac. This note lists what is and isn't possible per platform.
Items marked **[unverified]** are from prior knowledge, not tested here.

## 0. What was actually tested (this Mac, 2026-11-07)

| probe | result |
|---|---|
| `GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/aos` | **builds**, 17 MB static binary, no code changes |
| `adb devices` (platform-tools 37.0.0) | only `emulator-5554 offline`; no phone attached |
| `xcrun devicectl list devices` | no iPhone attached |
| `xcrun simctl` (iOS 26.5 sim, headless boot) | screenshot 1.8 s (1206x2622 PNG), `openurl`, `listapps`, `pbcopy/pbpaste` all work |
| `/System/Applications/iPhone Mirroring.app` | present (macOS 26.4) |

Not tested: anything on a real phone, uiautomator, WDA.

## 1. Android — aos can run on the device

Android is Linux. A Go binary runs; the question is **which user it runs as**, because that
decides what it may touch.

### 1a. aos as a shell binary (`/data/local/tmp/aos`, run via `adb shell`) — closest to "same as other OS"

Pushed over adb, it runs as uid `shell`, which holds the permissions the adb tools use. So aos
can do *inside the phone* what adb does from outside:

| aos group | on-device implementation (uid shell) | works? |
|---|---|---|
| `capture` screenshot | exec `screencap -p` (or SurfaceControl via app_process) | yes; FLAG_SECURE screens come out black |
| `capture` video | `screenrecord` (3 min cap, no audio before Android 11 audio via scrcpy only) | yes **[unverified limits per version]** |
| `mouse click/drag` | `input tap/swipe`, or write to `/dev/input/eventN` (shell can't on most devices) | yes, ~100–300 ms per `input` call (it spawns a JVM) |
| `key` | `input keyevent` | yes |
| type text | `input text` — **ASCII only**, spaces as `%s`; unicode needs an IME (ADBKeyBoard) or clipboard+paste | partial |
| `find` / `tab read` (element tree) | `uiautomator dump` → XML with text, resource-id, content-desc, bounds → aos refs | yes; 1–3 s per dump, fails while animations run **[unverified timing]** |
| `window list` / focused app | `dumpsys window` / `dumpsys activity activities` (parse) | yes |
| `launch` / `open` | `am start -n pkg/act`, `monkey -p pkg 1`, `am start -a VIEW -d <url>` | yes |
| `clipboard` | `cmd clipboard` doesn't exist for shell on 10+; read blocked for background apps since Android 10 | **mostly no** |
| `watch` notifications | `dumpsys notification --noredact` | read-only, yes |
| `process`, `file`, `exec`, `network` | normal Linux; `/sdcard` and `/data/local/tmp` writable | yes (sandboxed by SELinux) |
| `draw` / `subtitle` overlay | needs SYSTEM_ALERT_WINDOW = an app | no |
| `serve mcp` | listens on a port; reach via `adb forward tcp:…` or LAN | yes |

Survival: the process dies when the adb session ends unless started with `nohup`/`setsid`;
it does **not** survive reboot, and needs USB or wireless debugging on (Android 11+ pairs over
Wi‑Fi without a cable: `adb pair`). Android 14/15 tighten background exec and wireless-debugging
times out when Wi‑Fi changes **[unverified details]**.

This is exactly what scrcpy does (pushes `scrcpy-server.jar`, runs it via `app_process` as shell)
and what droidrun / mobile-mcp / Maestro do from the host side. Running aos *itself* there just
moves the brain onto the phone.

### 1b. aos inside Termux (no adb) — runs, but almost no GUI

Termux runs as an ordinary app uid. aos runs (`exec`, `file`, `process`, `network`), but
`input`, `screencap`, `uiautomator` are denied. GUI-first aos is useless here. (Termux:API adds
clipboard, notifications, toast — via a companion app.)

### 1c. aos as a real Android app (APK) — full "installed" experience

An app holding **AccessibilityService** (tree + tap/gesture/global actions + text set, unicode
fine) + **MediaProjection** (screenshots, user consent per session) + overlay permission
(`draw`, `subtitle`) covers every GUI group without a computer attached and survives reboot.
This is how droidrun-portal and all phone "agent" apps work. Cost: a Kotlin/Java shell (gomobile
or a JNI .so for the Go core), Play Store policy scrutiny for AccessibilityService, signing.
That is a second product, not "small aos".

## 2. iOS — aos cannot run as a CLI on a stock iPhone

Facts that close the door:
- No user shell, no `exec` of arbitrary binaries, everything is a signed, sandboxed app.
- No public API lets an app see or tap *other* apps' UI (no accessibility service equivalent).
- Screenshots of other apps: only ReplayKit broadcast with user consent.

What *can* exist on the device:

| option | what it gets | cost |
|---|---|---|
| **XCUITest runner app** (WebDriverAgent-style; go-ios can launch it) | tree, tap, type (unicode), screenshot, launch any app — of the whole device | dev account + signing; runner must be started from a Mac (`xcodebuild`/go-ios) each time, or kept alive; 7‑day free-profile expiry |
| aos as an iOS app (Swift + Go via gomobile) | its own sandbox only: files, network, Shortcuts/App Intents | can't drive other apps |
| Shortcuts / App Intents | launch apps, open URLs, clipboard | no tap, no tree |
| jailbreak | anything | out of scope |

So "aos on iOS" = an aos **runner app** built on XCTest (like WDA) that exposes aos's commands
(find/click/type/capture/launch) over a port, with the Mac doing setup. Truly standalone is
not possible.

From the Mac side (no install on phone): `xcrun simctl` for simulators (verified above),
`devicectl` for launch/install on real devices, and **iPhone Mirroring** (macOS 15+): a normal
Mac window aos already drives — click, type, screenshot. Its AX tree is opaque (one video
surface) **[unverified]**, so `find` would need OCR.

## 3. Feature matrix

| | Android shell aos (1a) | Android app (1c) | iOS XCUITest runner | iPhone Mirroring via mac aos | scrcpy window via host aos |
|---|---|---|---|---|---|
| runs on phone | yes | yes | yes (app) | no | no |
| needs computer to start | yes (adb) | no | yes (Mac) | Mac is the host | yes |
| element tree / find | uiautomator | a11y service | XCUI | OCR only | OCR / adb |
| tap / swipe | yes | yes | yes | yes | yes |
| unicode typing | no (IME trick) | yes | yes | yes | yes |
| screenshot | yes | yes (consent) | yes | yes | yes |
| launch app / URL | yes | yes | yes | via Spotlight | yes |
| clipboard | no | own only | partial | Mac's | yes (scrcpy syncs) |
| watch for text | poll dump | events | poll | poll OCR | poll |
| survives reboot | no | yes | no | n/a | n/a |
| new code in aos | small: android backend shelling to on-device tools | large: APK | large: Swift runner | ~zero | ~zero |
| setup | dev options + USB/wireless debugging | install APK, grant a11y + projection | Apple dev account, signing, trust | macOS 15, same Apple ID, iPhone locked nearby | adb + scrcpy |

## 4. Size / dependencies

- Android shell backend: no new Go deps; it execs `input`, `screencap`, `uiautomator`, `am`,
  `dumpsys` on the device. Binary already 17 MB for android/arm64. A host-side native adb
  wire-protocol client is unneeded if aos runs on the phone; for host→phone use, shelling to the
  `adb` binary is simpler than a Go adb lib (adb is already required for pairing).
- iOS runner: separate Xcode project; keep it out of the aos Go module.

## Sources
- Android `input`, `screencap`, `uiautomator`, `am`: https://developer.android.com/tools/adb (wireless pairing Android 11+)
- scrcpy server-as-shell design: https://github.com/Genymobile/scrcpy/blob/master/doc/develop.md
- AccessibilityService: https://developer.android.com/reference/android/accessibilityservice/AccessibilityService
- WebDriverAgent: https://github.com/appium/WebDriverAgent · go-ios: https://github.com/danielpaulus/go-ios
- simctl: `xcrun simctl help` (Xcode, iOS 26.5 runtime, tested 2026-11-07)
- iPhone Mirroring: https://support.apple.com/en-us/120421

## 5. Who already does this (web search 2026-11-07, not hands-on tested)

| project | runs where | platforms | how | gap vs aos |
|---|---|---|---|---|
| Open-AutoGLM (Zhipu / zai-org) | host Python + 9B model | Android | adb; tuned for 50+ Chinese apps | a model+agent, not a CLI; licence unclear (research-only vs Apache-2.0 reports) |
| AutoGLM-For-Android (community, MIT) | **on the phone**, app | Android | Shizuku + permissions; no computer after setup | closest to "aos installed on Android"; Shizuku = the shell-uid trick in app form |
| droidrun / Mobilerun | host Python + on-device "portal" app | Android, iOS | accessibility portal app | LLM agent framework, needs app on phone |
| mobile-mcp (Mobile Next, Apache-2.0) | host MCP server | Android, iOS, sims | adb / WDA | mobile-only; no desktop |
| agent-device (Callstack, MIT, Jan 2026) | host CLI | Android, iOS | adb / XCUITest | nearest CLI peer; mobile-only, testing-focused |
| native-devtools-mcp | host MCP | macOS, Windows, Chrome/Electron, Android | native APIs + adb | nearest overall peer: desktop+Android, no iOS |
| iPhone-MCP (blitzdotdev), mcp-ios (GPL-3), Mobai MCP, WebdriverIO MCP | host | iOS (+Android) | WebDriverAgent / Appium | all need Mac + dev signing |

Takeaways:
- **Nobody ships one small binary that runs on macOS/Windows/Linux *and* on the phone itself.**
  Everyone is either host-side (adb/WDA from a computer) or a phone app. aos running as uid shell on
  Android with the same command surface and `serve mcp` is an open slot.
- Every iOS option needs WebDriverAgent/XCTest + developer signing from a Mac. Apple's sandbox
  makes standalone on-device iOS control impossible for everyone, not just aos.
- Shizuku (AutoGLM-For-Android) is worth evaluating as the way to give an aos *APK* shell-level
  powers without keeping adb attached.

Sources: https://technode.com/2025/12/09/zhipu-ai-open-sources-autoglm-an-ai-agent-model-capable-of-full-phone-operation/ ·
https://www.sourcepulse.org/projects/21990844 · https://github.com/droidrun/mobilerun ·
https://www.opensourcealternatives.to/item/mobile-mcp · https://awesome.ecosyste.ms/projects/github.com%2Fcallstack%2Fagent-device ·
https://blog.brightcoding.dev/2026/11/01/sh3ll3x3cnative-devtools-mcp-cross-platform-native-app-automation ·
https://glama.ai/mcp/servers/dockndevai/mcp-ios · https://reddit.sentinel-team.org/posts/1riryys/snapshots/2026-03-04T15%3A12%3A56.583662Z

## 6. Capability check on a real Android 15 image (2026-11-07)

Emulator: AVD `kc35`, android-35 google_apis arm64, booted headless
(`-no-window -no-audio -no-boot-anim -no-snapshot -read-only -memory 1536 -cores 2 -gpu swiftshader_indirect -port 5560`).
Host: qemu RSS ~650 MB (vmmap "physical footprint" 5.7 GB, which includes guest RAM mappings);
the guest reported MemTotal 2.5 GB, so the AVD's own RAM setting overrode `-memory`.

aos (unmodified `GOOS=android` build, 16.9 MB) pushed to `/data/local/tmp/aos`, run via `adb shell`:

| probe | result | time |
|---|---|---|
| `id` | uid 2000 shell, **groups include `input`**, SELinux Enforcing, context u:r:shell:s0 | 63 ms |
| `aos version` / `system info` / `process list` | work (reports platform `linux`) | 70–95 ms |
| `aos window list` / `display list` | fail: Linux backend calls `wmctrl`/`xrandr` → needs an android backend | — |
| `screencap -p` | works, 1.37 MB PNG | 1.4 s |
| `uiautomator dump` | works, 12 KB XML | **2.5 s** (slowest part of a find loop) |
| `input tap` / `keyevent HOME` | work | 58–70 ms |
| `input text hello` | works | 225 ms |
| `input text 'héllo'` | **fails**: NullPointerException in InputShellCommand.sendText (no unicode) | — |
| `am start -a VIEW -d https://example.com` | works (opened Chrome first-run) | 136 ms |
| `dumpsys window` → `mFocusedApp` | works (`com.android.chrome/...FirstRunActivity`) | 104 ms |
| `cmd clipboard` | **"No shell command implementation."** → no clipboard | — |
| `dumpsys notification --noredact` | works (2 records) | 147 ms |
| `screenrecord --time-limit 2` | works, 650 KB mp4 | 2.4 s |
| `/dev/input/event*` | `crw-rw---- root input` and shell is in `input`, so aos can inject touch/key events **directly from Go** without spawning `input` (on this image; to verify on real phones) | — |

Conclusion: every GUI primitive aos needs exists for uid shell on Android 15 except unicode typing and
the clipboard. The work is an `android` backend in aos (build tag `android`, which Go already sets),
replacing the `wmctrl`/`xrandr` paths with `dumpsys`/`wm size`/`screencap`/`uiautomator`/`/dev/input`.

## 7. iOS capability check (2026-11-07, iOS 26.5 simulator, Xcode on macOS 26.4)

| probe | result |
|---|---|
| `GOOS=ios CGO_ENABLED=0 go build ./cmd/aos` | fails: ios/arm64 requires cgo (external linking) |
| same with cgo + iphonesimulator SDK | fails: GOOS=ios also matches the `darwin` build tag, so aos's macOS AppKit adapter (`-mmacosx-version-min`) is compiled in; needs `//go:build darwin && !ios` split |
| tiny Go binary built for iOS sim, `simctl spawn` | **runs** (uid 501, HOME = the sim's data dir, no `sh` on PATH) |
| `simctl io screenshot` / `openurl` / `launch` / `pbcopy`+`pbpaste` / `ui appearance` / `push` / `location` / `privacy` | work (sim only) |
| tap, type, element tree on sim | **not in simctl**; needs XCUITest (WebDriverAgent) or idb |
| real iPhone | none attached; `devicectl list devices` empty |

Meaning:
- On the **simulator** an aos binary can run, but it's really a Mac process in a sandbox dir; it can't see
  the iOS UI itself. GUI control still comes from outside (simctl + XCUITest).
- On a **real iPhone** an unsigned CLI cannot be executed at all. aos would have to ship as a signed
  XCUITest runner app (the WebDriverAgent pattern) whose test process holds the whole-device
  automation rights, exposes aos commands on a port, and must be launched from a Mac with Xcode
  or go-ios each time (free signing expires in 7 days; a paid account lasts a year).
- iPhone Mirroring (macOS 15+) stays the zero-code path: the existing mac backend drives its window.

## 8. How many of aos's 131 commands can exist on the phone (language-neutral)

Owner, 2026-11-07: the mobile builds need not be Go; Rust for both is fine. The limits below are the OS's,
not the language's. Rust fits both: an Android static binary (`aarch64-linux-android`, cargo-ndk) and an iOS
`staticlib` linked into a Swift XCUITest runner.

Columns:
- **A**: one binary run as uid shell (adb push, no app). Probed on the Android 15 emulator unless marked *.
- **A+**: the same binary plus a tiny helper APK. **uid shell can grant that APK its permissions with no user
  taps**: `appops set <pkg> SYSTEM_ALERT_WINDOW allow`, `settings put secure enabled_accessibility_services …`,
  `pm grant`, `ime set` (*unverified on real phones*).
- **I**: a signed iOS XCUITest runner app, started from a Mac (*all unverified*).

| group (n) | A | A+ | I | notes |
|---|---|---|---|---|
| adapters (3) | 3 | 3 | 3 | files |
| audio (2) | 2 | 2 | 2 | A: `cmd media_session volume`; I: volume buttons via XCUIDevice |
| batch (2) | 2 | 2 | 2 | |
| battery (3) | 3 | 3 | 3 | `dumpsys battery` (verified) / UIDevice |
| capture (10) | 8 | 8 | 1 | screenshot + `screenrecord` (verified); `zoom` re-render and ffmpeg device list stay on the host; iOS on-device video = ReplayKit consent |
| clipboard (2) | 0 | 2 | 1 | shell has no clipboard command (verified); helper set as IME can read it; iOS read prompts the user |
| completion, debug, doctor, version (5) | 5 | 5 | 5 | |
| display (3) | 3 | 3 | 3 | `wm size` (verified) |
| draw (5) | 2 | 5 | 2 | overlay needs the helper; iOS can't draw over other apps |
| exec (3) | 3 | 3 | 0 | iOS has no subprocesses |
| file (9) | 9 | 9 | 9 | iOS: app sandbox only |
| find (1) | 1 | 1 | 1 | uiautomator (2.5 s, verified) / a11y service (fast) / XCUI tree |
| font (2) | 2 | 2 | 2 | 208 files in /system/fonts (verified) |
| headless (2) | 0 | 0 | 0 | no meaning on a phone |
| key (2) | 2 | 2 | 2 | A: ASCII only (verified); A+ helper IME → unicode |
| launch, open (2) | 2 | 2 | 2 | `am start` (verified) |
| mouse (3) | 1 | 1 | 1 | only click/tap; no pointer to move or read |
| network (3) | 3 | 3 | 2 | `ip`, `cmd wifi status` (verified); iOS SSID needs an entitlement |
| obs (7) | 7 | 7 | 7 | |
| permission (2) | 2 | 2 | 2 | maps to appops/pm grant |
| pkg (7) | 4 | 4 | 0 | list/install/remove/manager via `pm`; no store CLI to search/upgrade; iOS can't list apps |
| power (1) | 1 | 1 | 1 | |
| process (3) | 3 | 3 | 1 | iOS: only terminate a known app |
| remote (2) | 2 | 2 | 2 | screenshot stream + taps (the WDA MJPEG pattern) |
| say (4) | 0 | 4 | 4 | TTS needs an app context |
| serve (2) | 2 | 2 | 2 | MCP on the phone; iOS only while the runner is alive |
| service (6) | 0 | 6 | 0 | shell can't register services; helper = foreground service; iOS has no daemons |
| skill (4) | 4 | 4 | 4 | trivial |
| subtitle (3) | 0 | 3 | 0 | overlay |
| system (6) | 5 | 5 | 2 | lock/sleep `input keyevent` (verified), restart/shutdown `svc power` (*); no logout |
| tab (8) | 7 | 7 | 4 | Chrome exposes CDP on the abstract socket `chrome_devtools_remote` (*), so list/open/close/focus/read/click/type work; `tab send` (terminal tabs) has no counterpart; iOS Safari via the a11y tree only |
| watch (4) | 3 | 4 | 2 | file, window (poll `dumpsys`), tab; clipboard needs the helper |
| webapp (1) | 1 | 1 | 0 | |
| window (9) | 4 | 4 | 3 | list (recents), focus (move task), text, wait; move/resize/arrange/save/zones only where split-screen/freeform exists |
| **total (131)** | **96** | **115** | **75** | |

Reading the totals: about 20 of each count are platform-neutral commands (obs, skill, batch, version…).
The GUI core (capture screenshot, find, key, mouse click, launch, open, window text/wait, tab read/click/type)
is complete on A for ASCII text, and complete with unicode on A+ and I.

## 9. The phone as an MCP server (verified on the emulator, 2026-11-07)

Owner's goal: commands are hard to type on a phone, so the phone's aos should be **exposed as MCP** and
driven from outside by an agent, with the aos skill describing it, the same way aos works everywhere.

Done with the unmodified build:
```
adb shell 'cd /data/local/tmp && HOME=/data/local/tmp nohup ./aos serve mcp --addr=127.0.0.1:9000 > mcp.log 2>&1 &'
adb forward tcp:19000 tcp:9000
claude mcp add --transport http phone http://127.0.0.1:19000/mcp --header "Authorization: Bearer <token from mcp.log>"
```
- The server starts on the phone with a bearer token and **110 tools**, and keeps running after the adb session ends.
- From the Mac: `tools/list` returned all 110; `system_info` returned the phone's kernel; `exec_shell`
  ran `input keyevent HOME` on the phone. `window_list` fails until the android backend exists.
- Transport options: `adb forward` over USB (no network exposure; preferred), wireless adb, or `--addr=0.0.0.0` on the LAN
  (token required; that is the remote-control case ADR 0001 declined, so it is opt-in only).
- iOS has the same shape: the XCUITest runner listens on a port (as WebDriverAgent does on 8100) and the Mac
  reaches it with `iproxy`/go-ios forward or the iOS 17+ tunnel; the runner speaks MCP instead of WebDriver.

So the product is: **an aos MCP server on the phone + the aos skill on the agent side**. The tool list adapts to
what the platform can do (unsupported tools hidden or exit 2), and the agent never types commands on the phone.
