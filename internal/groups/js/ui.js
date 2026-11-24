// Drive an app's window: open it, click by text, wait for text to appear.

/**
 * Launch an app (or focus it when already running) and wait until its window
 * exists. Returns the window object {ID, Title, App, Bounds, Monitor, ...}
 * or null when no window appeared within timeoutMs (default 10000).
 */
export async function openApp(app, timeoutMs = 10000) {
  await ops["launch"]({ args: [app], "or-focus": true })
  try {
    await ops["window-wait"]({ args: [app], timeout: timeoutMs })
  } catch (e) {
    return null
  }
  const wins = await ops["window-list"]({ app })
  return wins.length > 0 ? wins[0] : null
}

/**
 * True when text shows up in the app's window within timeoutSec (default 10),
 * false when it does not. Looks at the window's text (accessibility, OCR fallback).
 */
export async function waitForText(app, text, timeoutSec = 10) {
  try {
    await ops["watch-window"]({ app, match: text, timeout: timeoutSec + "s" })
    return true
  } catch (e) {
    return false
  }
}

/**
 * Click the control labelled `text` in an app, then confirm the result:
 * when `expect` is given, wait up to timeoutSec (default 10) for that text.
 * Returns {clicked: true, appeared: true|false|null}; appeared is null when
 * no expect was given. Rejects when the control is not found.
 */
export async function clickThenExpect(app, text, expect, timeoutSec = 10) {
  await ops["mouse-click"]({ app, find: text, focus: true })
  const appeared = expect ? await waitForText(app, expect, timeoutSec) : null
  return { clicked: true, appeared }
}
