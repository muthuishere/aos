package engine

import (
	"html"
	"strconv"
	"strings"
)

// remoteViewerHTML renders the single-page viewer. token is embedded so
// every /frame and /input request carries it; pollMS is the frame poll
// interval. The page maps a click on the displayed image back to the
// true screen point using the frame's natural pixel size and the
// origin headers (screenshots are point-normalized: 1 image pixel == 1
// screen point, so screenX = originX + naturalPixelX).
func remoteViewerHTML(token string, pollMS int, title string) string {
	// Placeholder replacement (not Sprintf) because the embedded CSS/JS
	// is full of literal % signs. The token is server-minted hex, safe
	// to inline as a JS string literal.
	r := strings.NewReplacer(
		"__TOKEN__", token,
		"__TITLE__", html.EscapeString(title),
		"__POLL__", strconv.Itoa(pollMS),
	)
	return r.Replace(viewerTemplate)
}

const viewerTemplate = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=6, user-scalable=yes">
<title>__TITLE__</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body { margin: 0; background: #0b0d10; color: #e6e6e6; font: 13px/1.4 system-ui, sans-serif; }
  header { display: flex; gap: 12px; align-items: center; padding: 8px 12px; background: #14171c; border-bottom: 1px solid #23272e; position: sticky; top: 0; z-index: 2; flex-wrap: wrap; }
  header b { color: #7cc4ff; }
  select, button { background: #1d2127; color: #e6e6e6; border: 1px solid #2c313a; border-radius: 6px; padding: 4px 8px; font: inherit; }
  button:hover { border-color: #3d84c6; }
  .dot { width: 8px; height: 8px; border-radius: 50%; background: #35c46a; display: inline-block; }
  .wrap { padding: 12px; display: flex; justify-content: center; }
  /* touch-action: manipulation keeps pinch-zoom + pan working (so you
     can zoom in on a phone to see detail) while disabling the 300ms
     double-tap-zoom, so our double-tap = double-click still fires. */
  #screen { max-width: 100%; height: auto; border: 1px solid #23272e; border-radius: 6px; cursor: crosshair; box-shadow: 0 8px 40px rgba(0,0,0,.5); touch-action: manipulation; -webkit-user-select: none; user-select: none; -webkit-touch-callout: none; }
  #hint { color: #8a929e; }
  #status { color: #8a929e; margin-left: auto; }
  kbd { background: #23272e; border-radius: 4px; padding: 1px 5px; border: 1px solid #333; }
  /* Real, VISIBLE input — mobile browsers refuse to raise the soft
     keyboard for a hidden / zero-size / opacity:0 field, so this must
     stay on screen and tappable. */
  #kin { min-width: 180px; flex: 1 1 180px; padding: 6px 10px; background: #1d2127;
         color: #e6e6e6; border: 1px solid #3d84c6; border-radius: 6px; font: inherit; }
  #kin::placeholder { color: #6b7480; }
  #kbd.active { border-color: #35c46a; color: #35c46a; }
</style>
</head>
<body>
<header>
  <span class="dot"></span><b>__TITLE__</b>
  <label>Monitor <select id="mon"></select></label>
  <label><input type="checkbox" id="ctl" checked> control</label>
  <button id="kbd" type="button" title="focus the type box (raises the keyboard on mobile)">⌨</button>
  <input id="kin" placeholder="tap here to type into the remote →"
         autocapitalize="off" autocomplete="off" autocorrect="off" spellcheck="false" enterkeyhint="enter">
  <span id="status">connecting…</span>
</header>
<div class="wrap"><img id="screen" alt="remote screen"></div>
<div id="hint" style="text-align:center;color:#8a929e;padding:0 12px 12px">tap / click = left · long-press = right · double-tap = double-click · type in the box above</div>

<script>
const TOKEN = "__TOKEN__";
const POLL = __POLL__;
const img = document.getElementById('screen');
const monSel = document.getElementById('mon');
const ctl = document.getElementById('ctl');
const status = document.getElementById('status');
let originX = 0, originY = 0, natW = 0, natH = 0;
let monitor = '';
let mons = [];

// Geometry for click mapping comes from /monitors (not per-frame
// headers, which an MJPEG <img> stream can't expose to JS). Because
// captures are point-normalized, a monitor's Width/Height IS the frame's
// natural pixel size and its X/Y is the frame origin.
function applyGeometry() {
  const m = mons.find(x => String(x.ID) === String(monitor)) || mons[0];
  if (m) { originX = m.X; originY = m.Y; natW = m.Width; natH = m.Height; }
}

async function loadMonitors() {
  try {
    const r = await fetch('/monitors?t=' + TOKEN);
    mons = await r.json() || [];
    monSel.innerHTML = '';
    mons.forEach(m => {
      const o = document.createElement('option');
      o.value = m.ID;
      o.textContent = 'Monitor ' + m.ID + ' (' + m.Width + '×' + m.Height + ')' + (m.Focused ? ' • focused' : '');
      if (m.Focused) o.selected = true;
      monSel.appendChild(o);
    });
    monitor = monSel.value || '';
    applyGeometry();
  } catch (e) { /* single-monitor / race — stream still works */ }
}
monSel.addEventListener('change', () => { monitor = monSel.value; applyGeometry(); startStream(); });

// Live MJPEG stream: the browser renders multipart/x-mixed-replace
// natively in the <img>, so frames arrive pushed and continuous.
function startStream() {
  const u = '/stream?t=' + TOKEN + (monitor ? '&monitor=' + monitor : '') + '&_=' + Date.now();
  img.src = u;
  status.textContent = 'live • ' + natW + '×' + natH;
}
img.addEventListener('error', () => { status.textContent = 'reconnecting…'; setTimeout(startStream, 1000); });
img.addEventListener('load', () => { if (natW) status.textContent = 'live • ' + natW + '×' + natH; });

// toScreen maps a client (clientX, clientY) point — from either a mouse
// event or a touch point — to the true screen coordinate, using the
// displayed image rect and the point-normalization guarantee.
function toScreen(clientX, clientY) {
  const rect = img.getBoundingClientRect();
  const sx = (clientX - rect.left) / rect.width;
  const sy = (clientY - rect.top) / rect.height;
  return { x: originX + Math.round(sx * natW), y: originY + Math.round(sy * natH) };
}

async function send(action) {
  if (!ctl.checked) return;
  try {
    await fetch('/input?t=' + TOKEN, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(action),
    });
  } catch (e) {}
  // No manual refresh — the MJPEG stream updates the image continuously.
}

// --- Mouse control ---
img.addEventListener('click', ev => {
  const p = toScreen(ev.clientX, ev.clientY);
  send({ type: 'click', x: p.x, y: p.y, button: ev.shiftKey ? 'right' : 'left' });
});
img.addEventListener('dblclick', ev => {
  const p = toScreen(ev.clientX, ev.clientY);
  send({ type: 'click', x: p.x, y: p.y, button: 'left', double: true });
});
img.addEventListener('contextmenu', ev => {
  ev.preventDefault();
  const p = toScreen(ev.clientX, ev.clientY);
  send({ type: 'click', x: p.x, y: p.y, button: 'right' });
});

// --- Touch control (phone / tablet) ---
// Two-finger gestures are left entirely to the browser (pinch-zoom /
// pan) — we only act on SINGLE-finger touches, and never preventDefault
// on touchstart/touchmove, so native zoom keeps working. A quick tap =
// left click; a long-press (>500ms, no move) = right-click; a second
// tap within 300ms = double-click. Only touchend preventDefaults, and
// only for a real tap, to suppress the 300ms-late synthetic mouse click
// (avoids a double). A finger that moves is a pan/zoom — ignored.
let touchTimer = null, touchStart = null, lastTapAt = 0, movedFar = false, multiTouch = false;
img.addEventListener('touchstart', ev => {
  if (ev.touches.length > 1) { multiTouch = true; touchStart = null; if (touchTimer) { clearTimeout(touchTimer); touchTimer = null; } return; }
  multiTouch = false;
  const t = ev.touches[0];
  touchStart = { x: t.clientX, y: t.clientY };
  movedFar = false;
  touchTimer = setTimeout(() => {
    touchTimer = null;
    if (!touchStart) return;
    const p = toScreen(touchStart.x, touchStart.y);
    send({ type: 'click', x: p.x, y: p.y, button: 'right' });  // long-press → right-click
    touchStart = null;
  }, 500);
}, { passive: true });
img.addEventListener('touchmove', ev => {
  if (ev.touches.length > 1) { multiTouch = true; }
  if (!touchStart || ev.touches.length !== 1) return;
  const t = ev.touches[0];
  if (Math.abs(t.clientX - touchStart.x) > 12 || Math.abs(t.clientY - touchStart.y) > 12) {
    movedFar = true;
    if (touchTimer) { clearTimeout(touchTimer); touchTimer = null; }
  }
}, { passive: true });
img.addEventListener('touchend', ev => {
  if (touchTimer) { clearTimeout(touchTimer); touchTimer = null; }
  if (multiTouch || !touchStart || movedFar) { touchStart = null; return; }
  ev.preventDefault(); // suppress the synthetic mouse click for this tap
  const p = toScreen(touchStart.x, touchStart.y);
  const now = Date.now();
  const isDouble = (now - lastTapAt) < 300;
  lastTapAt = now;
  send({ type: 'click', x: p.x, y: p.y, button: 'left', double: isDouble });
  touchStart = null;
}, { passive: false });

// Named keys and chords go through /input type:"key"; plain printable
// characters go through type:"text" so unicode is preserved.
const NAMED = { 'Enter':'enter','Tab':'tab','Escape':'esc','Backspace':'backspace','Delete':'delete',
  'ArrowUp':'up','ArrowDown':'down','ArrowLeft':'left','ArrowRight':'right',
  'Home':'home','End':'end','PageUp':'pageup','PageDown':'pagedown',' ':'space' };
// --- Mobile soft keyboard (Chrome Android / iOS Safari) ---
// The type box (#kin) is a REAL visible input — mobile browsers won't
// raise the keyboard for a hidden/zero-size field. Tap it (or the ⌨
// button) and the soft keyboard appears.
//
// Chrome Android reports keyCode 229 / key "Unidentified" for character
// keys, so we CANNOT read typed characters from keydown — the text only
// arrives via the 'input' event (and via composition events for
// predictive / swipe typing). So: characters come from input/
// compositionend; only real-keyCode special keys (Enter, Backspace,
// arrows on a physical keyboard) come from keydown. The field is cleared
// after every event so it never accumulates and Backspace-on-empty
// still fires cleanly.
const kin = document.getElementById('kin');
const kbdBtn = document.getElementById('kbd');
kbdBtn.addEventListener('click', () => { kin.focus(); kbdBtn.classList.add('active'); });
kin.addEventListener('focus', () => kbdBtn.classList.add('active'));
kin.addEventListener('blur', () => kbdBtn.classList.remove('active'));

let composing = false;
kin.addEventListener('compositionstart', () => { composing = true; });
kin.addEventListener('compositionend', ev => {
  composing = false;
  if (ev.data) send({ type: 'text', text: ev.data });
  kin.value = '';
});
kin.addEventListener('input', ev => {
  if (composing || ev.isComposing) return; // wait for compositionend to get final text
  const it = ev.inputType || '';
  if (it === 'deleteContentBackward') { send({ type: 'key', combo: 'backspace' }); kin.value = ''; return; }
  if (it === 'deleteContentForward')  { send({ type: 'key', combo: 'delete' });    kin.value = ''; return; }
  if (it === 'insertLineBreak' || it === 'insertParagraph') { send({ type: 'key', combo: 'enter' }); kin.value = ''; return; }
  const t = (ev.data != null) ? ev.data : kin.value;
  if (t) send({ type: 'text', text: t });
  kin.value = '';
});
kin.addEventListener('keydown', ev => {
  // 229 / isComposing = soft-keyboard character input — leave it to the
  // input event. Only real special keys are handled here.
  if (ev.keyCode === 229 || ev.isComposing) return;
  if (NAMED[ev.key] && ev.key !== ' ') {
    ev.preventDefault();
    send({ type: 'key', combo: NAMED[ev.key] });
  }
});

document.addEventListener('keydown', ev => {
  if (!ctl.checked) return;
  if (ev.target === kin || ev.target.tagName === 'SELECT') return;
  if (ev.target.tagName === 'INPUT') return;
  const mods = [];
  if (ev.metaKey) mods.push('cmd');
  if (ev.ctrlKey) mods.push('ctrl');
  if (ev.altKey) mods.push('alt');
  if (ev.shiftKey) mods.push('shift');
  let key = null;
  if (NAMED[ev.key]) key = NAMED[ev.key];
  else if (ev.key.length === 1) key = ev.key.toLowerCase();
  if (!key) return;
  // A bare printable char with no non-shift modifier: type it literally
  // so we don't lose the exact glyph to keycode mapping.
  const onlyShift = mods.length === 0 || (mods.length === 1 && mods[0] === 'shift');
  if (ev.key.length === 1 && onlyShift && !NAMED[ev.key]) {
    ev.preventDefault();
    send({ type: 'text', text: ev.key });
    return;
  }
  ev.preventDefault();
  send({ type: 'key', combo: mods.concat([key]).join('+') });
});

loadMonitors().then(startStream);
</script>
</body>
</html>`
