# ADR 0002: aos on phones

- Status: **Proposed**
- Date: 2026-11-07
- Research: `docs/research/mobile.md`

## Context

The owner wants aos installed on Android and iOS and to behave there as on the desktop OSes.
aos already cross-compiles to android/arm64 (17 MB, CGO off). iOS allows no CLI and no
cross-app control except through XCTest.

## Decision (proposed)

1. **Android first, as an OS backend.** Add `GOOS=android` implementations of the GUI groups
   that exec on-device tools (`input`, `screencap`, `screenrecord`, `uiautomator dump`, `am`,
   `dumpsys`). aos is pushed to `/data/local/tmp` and runs as uid shell; `aos serve mcp` on the
   phone exposes it. Unsupported groups (`draw`, `subtitle`, clipboard) exit 2, as on other OSes.
   A tiny helper `aos device install` on the host (adb push + start) does the one-time setup.
2. **iOS: not as a CLI.** Short term, drive it from the Mac via iPhone Mirroring (existing
   mac backend, zero code) and `simctl` for simulators. A native XCUITest runner app is a
   separate project, decided later.
3. **Android APK (AccessibilityService)** only if reboot-survival without a computer turns out to
   matter; it is a separate product.

## Consequences

- Android support is small and agentless beyond aos itself, but needs developer options and USB
  or wireless debugging, and does not survive reboot.
- Unicode typing on Android needs an IME or clipboard path; ASCII works.
- iOS will never be at parity without a signed runner app.

## Spike plan (cheapest first)

1. Push the android build to the emulator/phone, run `aos version`, `aos process list`, `aos system info`.
2. Map `uiautomator dump` XML → aos `find` refs; `aos mouse click` → `input tap` on a found ref.
3. `aos serve mcp` on the phone + `adb forward`; drive from Claude.
4. iOS: open iPhone Mirroring, `aos window list`, `aos capture`, `aos find` (check if AX tree is empty).

## Update 2026-11-07 (owner direction)

- The mobile builds may be Rust instead of Go: Android static binary, iOS staticlib inside a Swift XCUITest runner.
- The primary interface is **MCP served from the phone** (`serve mcp` behind `adb forward` / iOS port forward),
  driven by an outside agent with the aos skill. Verified on the Android 15 emulator: 110 tools listed, calls work.
- Coverage target (research §8): Android shell-only 96/131 commands, Android + helper APK 115/131, iOS runner ~75/131.

## Distribution (2026-11-07, not verified against current store policy text)

| piece | channel | verdict |
|---|---|---|
| Android shell binary (96/131) | aos releases + `aos device install` (adb push) | no store needed, no review |
| Android helper APK, AccessibilityService (115/131) | GitHub / F-Droid first; Play only with the accessibility declaration, disclosure and demo video | possible, hard review; Tasker/MacroDroid are precedent. Remote control must stay USB/`adb forward` + token, no arbitrary exec, to avoid device-abuse / downloaded-code policies |
| Android sideloaded APK | Google developer verification (rolling out from 2026) | needs a registered developer; adb installs reportedly exempt (unverified) |
| iOS XCUITest runner (~75/131) | App Store / TestFlight / EU marketplaces | **not allowed**: test bundles can't ship and apps may not drive other apps. Only route is build-it-yourself with the person's own Apple ID (7-day free / 1-year paid signing) |
| iOS sandboxed aos app (Shortcuts, files, URL, TTS, battery) | App Store | allowed, but ~20–30 commands and no screen control |

## Owner's take (2026-11-07)

Not convinced this is good: Android is fine without a store, but the store paths are policy-fragile and iOS
cannot ship through any store. Parked as **Proposed**, revisit later. No code until the owner decides.
