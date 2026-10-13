// Builds every image the site shows in a lightbox: the landing "What it does"
// card thumbnails (public/cards/) and the scenario page shots (public/shots/).
// Each image is written twice: <name>.webp at 640px wide for the page, and
// full/<name>.webp at full resolution for the lightbox.
//
// Two sources, and only these two:
//   - stills from public/demo/aos-demo.mp4 (ffmpeg) at the video's native
//     1920x1080;
//   - terminal-style renders of real command output (headless Chrome), at 1x
//     for the thumbnail and 2x for the full image. The text in TERMINALS below
//     is the verbatim output of the command on the machine that built it, with
//     its own paths, host name, address and network name replaced.
// Nothing here opens, moves, draws on or records the screen.
//
// Run by hand when the demo or the output changes:
//   node scripts/make-images.mjs
// Needs ffmpeg and Google Chrome.

import { execFileSync } from 'node:child_process';
import { mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import sharp from 'sharp';

const here = dirname(fileURLToPath(import.meta.url));
const site = resolve(here, '..');
const video = join(site, 'public', 'demo', 'aos-demo.mp4');
const work = join(tmpdir(), 'aos-images');
mkdirSync(work, { recursive: true });

const CHROME = process.env.CHROME || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';

// ---------------------------------------------------------------------------
// Landing cards: name -> second in the demo, or [prompt, output lines].
// ---------------------------------------------------------------------------
const CARD_STILLS = { windows: 35, launch: 40, find: 55, typing: 70, labels: 90, point: 105, video: 120 };

const T = {
	screenshot: ['aos capture screenshot --app=Finder --out=shot.png', ['/Users/you/shot.png  1730x933+2045+94']],
	shell: ['aos exec capture -- git status --short', ['{', '  "command": ["git", "status", "--short"],', '  "exit": 0,', '  "stdout": "",', '  "stderr": "",', '  "duration_ms": 14', '}']],
	watch: ['aos watch clipboard --max=1', ['{"event":"clipboard","at":"2026-10-13T06:36:30+05:30",', ' "seq":1,"length":14,"digest":"4b559aab5a29"}']],
	clipboard: ['aos clipboard copy "hello from aos" && aos clipboard paste', ['hello from aos']],
	process: ['aos process find Finder', ['   2424    8.0      108.9MB  you  Finder', '', '$ aos service list', 'no services']],
	files: ['aos file read site/package.json --lines=1:6', ['{', '  "name": "aos-site",', '  "type": "module",', '  "version": "0.0.1",', '  "scripts": {', '    "dev": "npm run gen && astro dev",']],
	system: ['aos audio volume', ['82', '', '$ aos battery status', '100%  charged', '', '$ aos network status', 'interface  en0', 'address    192.168.1.24', 'wifi       HomeNet']],
	audit: ['aos obs stats --since=24h', ['route                calls  errors   p50ms   p95ms', 'key press              157      20      81     313', 'window list            139      11      25      44', 'window move            125      52     121     201', 'key type                94       5     306    1273', 'capture screenshot      66       0     117     137', 'draw                    63       8     226   13247']],
	adapters: ['aos adapters example', ['{', '  "group": "my",', '  "description": "My own commands",', '  "commands": [', '    {', '      "name": "hello",', '      "summary": "Say hello, to prove adapters work",', '      "args": "[name]",', '      "run": "echo hello"', '    }']],
	remote: ['aos remote --help', ["remote — Hand this desktop's screen and input", '         to a browser on the LAN', '', 'Commands:', '  aos remote share [--monitor=<n>] [--port=<n>]', '                   [--fps=<n>] [--key=<k>]', '  aos remote url']],
	mcp: ['aos serve tools --groups=window', ['window_arrange   Apply a saved layout of many windows', 'window_focus     Bring a window to the front', 'window_list      List open windows', 'window_move      Move a window to a zone, a split, or bounds', 'window_resize    Resize a window in place', 'window_save      Save the current layout to a file']],
};

// ---------------------------------------------------------------------------
// Scenario shots: name -> second in the demo, or [prompt, output lines].
// ---------------------------------------------------------------------------
const SHOT_STILLS = {
	'layout-before': 33,
	'layout-after': 35,
	'meeting-safari': 40,
	'form-type': 70,
	'form-verify': 73,
	'button-find': 52,
	'button-press': 55,
	'webapp': 40,
	'arrow-done': 105,
	'arrow-windows': 110,
	'subtitle': 90,
	'demo-rec': 115,
	'demo-camera': 120,
	'bug-rec': 117,
};

const SHOT_TERMINALS = {
	'zones': ['aos window zones', ['monitor 2  1920x1080+0+0', '', '  1:1    1920x1080+0+0', '  1A     960x1080+0+0', '  1B     960x1080+960+0', '  2A     960x540+0+0', '  2B     960x540+960+0', '  2C     960x540+0+540', '  2D     960x540+960+540', '  1:3    640x1080+0+0', '  2:3    640x1080+640+0', '  3:3    640x1080+1280+0', '', '  M:N   the M-th of N: 1:3 is the left third, 2:3 the middle, 3:3 the right']],
	'display-list': ['aos display list', ['1  1728x1117-1728+0', '2  1920x1080+0+0  [primary active focused]', '3  1920x1080+1920+0', '', '$ aos window list', '* Ghostty          mon 2  1920x971+0+30     you: zsh', '  Google Chrome    mon 3  1920x960+1920+30  aos docs', '  Calculator       mon 1  230x408-928+80    Calculator', '  Finder           mon 3  1730x933+2045+94  os-cli']],
	'screenshot-out': ['aos capture screenshot --app=Finder --out=/tmp/shot.png', ['/tmp/shot.png  1730x933+2045+94', '', '$ aos permission check', 'accessibility   granted', 'screen capture  granted']],
	'remote-url': ['aos remote url', ['aos: no share is running; start one with `aos remote share`', '', '$ aos remote share --help', 'aos remote share — Share this screen and its mouse/keyboard', '                   over the LAN until interrupted', '', 'Usage:', '  aos remote share [--monitor=<n>] [--port=<n>] [--fps=<n>] [--key=<k>]']],
	'watch-clipboard': ['aos watch clipboard --max=1 --content', ['{"event":"clipboard","at":"2026-10-13T06:36:30+05:30","seq":1,', ' "length":14,"digest":"4b559aab5a29","text":"hello from aos"}']],
	'watch-file': ['aos watch file ~/Downloads --max=1', ['{"event":"file","at":"2026-10-13T06:46:53+05:30","seq":1,', ' "kind":"created","path":"/Users/you/Downloads/new.txt","size":0}']],
	'service': ['aos service list', ['no services', '', '$ aos service status mcp', 'name    mcp', 'label   aos.mcp', 'state   not-installed', '', '$ aos service create mcp --autostart --now -- aos serve mcp']],
	'kill': ['aos process find Calculator', ['  86251    0.0       40.4MB  you  Calculator', '', '$ aos process kill Chrome', 'aos: "Chrome" matches 38 processes; kill one by pid, or narrow the name:', '  7318  Google Chrome', '  7337  chrome_crashpad_handler', '  7347  Google Chrome Helper', '  ... and 35 more']],
	'doctor': ['aos doctor', ['ok   registry     118 of 120 commands available on darwin', 'ok   telemetry    recording to /Users/you/.local/state/aos/telemetry', 'ok   display      available; GUI commands will run', 'ok   permissions  accessibility and screen capture granted', 'ok   windows      21 windows visible', 'ok   screenshot   captured 2089197 bytes', 'ok   video        ffmpeg ok; 6 audio input(s); system audio via BlackHole 2ch', 'ok   clipboard    round trip succeeded', 'ok   packages     homebrew', 'ok   plugins      0 external commands across 38 directories']],
	'hardware': ['aos battery status', ['100%  charged', '', '$ aos power status', 'ac', '', '$ aos network status', 'interface  en0', 'address    192.168.1.24', 'wifi       HomeNet', '', '$ aos system info', 'arch        arm64', 'cpu         Apple M5 Pro', 'os          macOS', 'os_version  26.4']],
	'audio': ['aos audio volume', ['82', '', '$ aos audio mute', 'off', '', '$ aos audio mute off && aos audio volume 40', '40']],
	'system-group': ['aos system --help', ['system — Session power state: lock, sleep, restart, shutdown, logout', '', 'Commands:', '  aos system info      Print OS, host, and hardware facts', '  aos system lock      Lock the screen', '  aos system logout    Log the current user out', '  aos system restart   Restart the machine', '  aos system shutdown  Power the machine off', '  aos system sleep     Put the machine to sleep']],
	'headless': ['aos headless status', ['platform     darwin', 'virtual      not available on this platform', 'note         desktop commands need a logged-in session here', '', 'display      yes — window, mouse, key, capture, and display commands will work', '', '$ ssh server aos window list', 'aos: "window list" needs a display, and this machine has none', 'Start one with `aos headless start`, or check `aos headless status`.']],
	'serve-tools': ['aos serve tools --groups=window,capture', ['capture_screenshot      Take a screenshot', 'capture_video_start     Start recording the screen with ffmpeg', 'capture_video_stop      Stop recording, finalize the mp4 and render', 'window_arrange          Apply a saved layout of many windows in one pass', 'window_focus            Bring a window to the front', 'window_list             List open windows', 'window_move             Move a window to a zone, a split, or absolute bounds', 'window_resize           Resize a window in place']],
	'adapter-list': ['aos adapters path', ['/Users/you/.config/aos/adapters', '', '$ aos adapters list', 'no adapters in /Users/you/.config/aos/adapters', 'run `aos adapters example --write` to start one', '', '$ aos skill path', 'claude   /Users/you/.claude/skills/aos', 'agents   /Users/you/.agents/skills/aos']],
	'obs-tail': ['aos obs tail --limit=8', ['06:55:20  err  cli         0ms  service status', '06:55:20  ok   cli        56ms  process find', '06:55:21  err  cli        47ms  process kill', '06:55:21  ok   cli       351ms  doctor', '06:55:21  ok   cli        11ms  battery status', '06:55:21  ok   cli         7ms  power status', '06:55:21  ok   cli        36ms  system info', '06:55:21  ok   cli         0ms  headless status']],
	'commands-list': ['aos commands | head -8', ['  adapters example       Print a starter adapter, ready to save and edit', '  adapters list          List the adapters this machine has loaded', '  adapters path          Print the directory adapters are read from', '  audio mute             Print or change the output mute state', '  audio volume           Print the output volume, or set it', '  battery percent        Print the charge percentage as a bare number', '  battery present        Exit 0 when a battery is present, 1 otherwise', '  battery status         Print battery charge and charging state']],
	'skill-show': ['aos skill show | head -8', ['---', 'name: aos', 'description: >', '  Drive the machine you are on — macOS, Windows or Linux — through one CLI:', '  windows, mouse and keyboard, screenshots, files, processes, shell commands,', '  packages, audio, clipboard, services, on-screen captions, and a LAN', '  screen-share you can watch from a phone. Trigger on: move/resize/focus', '  a window, list windows or monitors, take a screenshot, …']],
	'file-delete-refused': ['aos file delete /', ['aos: refusing to delete /', '', '$ aos file delete $HOME', 'aos: refusing to delete /Users/you', '', '$ echo $?', '1']],
	'window-list': ['aos window list', ['* Ghostty          mon 2  1920x971+0+30     you: zsh', '  QuickTime Player mon 2  1744x980+50+30    take2.mp4', '  Google Chrome    mon 3  1920x960+1920+30  aos docs', '  Calculator       mon 1  230x408-928+80    Calculator', '  Finder           mon 3  1730x933+2045+94  os-cli', '  Finder           mon 2  920x492+576+149   Downloads']],
	'commands-json': ['aos commands --json | head -c 300', ['{', '  "ok": true,', '  "platform": "darwin",', '  "commands": [', '    {', '      "route": "aos adapters example",', '      "group": "adapters",', '      "summary": "Print a starter adapter, ready to save and edit",', '      "args": "[--write]",', '      "available": true,', '      "needs_display": false,']],
	'obs-failed': ['aos obs audit --since=1h --failed', ['2026-10-13 06:02:59  cli  commands        0ms  FAILED exit 1', '2026-10-13 06:03:25  cli  capture         0ms  FAILED exit 1', '2026-10-13 06:46:54  cli  process kill   44ms  FAILED exit 1', '2026-10-13 06:55:20  cli  service status  0ms  FAILED exit 1', '4 entries']],
};

const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

function terminalHtml(prompt, lines) {
	const body = lines.map((l) => (l.startsWith('$ ') ? `<b>$</b> <span class="p">${esc(l.slice(2))}</span>` : esc(l))).join('\n');
	return `<!doctype html><meta charset="utf-8"><style>
html,body{margin:0;background:#07080a}
.t{width:960px;height:540px;box-sizing:border-box;padding:22px 28px;background:#07080a;color:#e6e8ec;
font:26px/1.45 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;position:relative;overflow:hidden}
.bar{display:flex;gap:10px;margin-bottom:18px}.bar i{width:16px;height:16px;border-radius:50%;background:#ffffff26}
pre{margin:0;white-space:pre-wrap;overflow-wrap:anywhere}
b{color:#8b85ff;font-weight:600}.p{color:#f2f3f5}
.fade{position:absolute;left:0;right:0;bottom:0;height:70px;background:linear-gradient(#07080a00,#07080a)}
</style><div class="t"><div class="bar"><i></i><i></i><i></i></div>
<pre><b>$</b> <span class="p">${esc(prompt)}</span>
${body}</pre><div class="fade"></div></div>`;
}

async function write(png, dir, name) {
	mkdirSync(join(dir, 'full'), { recursive: true });
	await sharp(png).resize({ width: 640 }).webp({ quality: 78 }).toFile(join(dir, `${name}.webp`));
	await sharp(png).webp({ quality: 82 }).toFile(join(dir, 'full', `${name}.webp`));
}

async function still(name, sec, dir) {
	const png = join(work, `${name}.png`);
	execFileSync('ffmpeg', ['-v', 'error', '-y', '-ss', String(sec), '-i', video, '-frames:v', '1', png]);
	await write(png, dir, name);
	console.log(`images: ${dir.split('/').pop()}/${name} from demo @ ${sec}s`);
}

function chrome(html, png, scale) {
	execFileSync(CHROME, ['--headless=new', '--hide-scrollbars', `--force-device-scale-factor=${scale}`, `--screenshot=${png}`, '--window-size=960,540', `file://${html}`], { stdio: 'ignore' });
}

async function terminal(name, [prompt, lines], dir) {
	const html = join(work, `${name}.html`);
	writeFileSync(html, terminalHtml(prompt, lines));
	const png1 = join(work, `${name}-1x.png`);
	const png2 = join(work, `${name}-2x.png`);
	chrome(html, png1, 1);
	chrome(html, png2, 2);
	mkdirSync(join(dir, 'full'), { recursive: true });
	await sharp(png1).resize({ width: 640 }).webp({ quality: 78 }).toFile(join(dir, `${name}.webp`));
	await sharp(png2).webp({ quality: 82 }).toFile(join(dir, 'full', `${name}.webp`));
	console.log(`images: ${dir.split('/').pop()}/${name} rendered from terminal output`);
}

// ---------------------------------------------------------------------------
// Overlay previews for the drawing pages. `aos draw`, `aos draw text` and
// `aos subtitle show` take --preview=<png>, which renders the overlay to a
// transparent 1920x1080 PNG and never touches the real screen. Each one is
// composited onto a neutral desktop drawn below, so the result reads as a
// screen without showing anyone's real desktop.
// ---------------------------------------------------------------------------
const WIN = '560,240,800,520'; // the fake window on the desktop below
const BTN = '860,576,200,56'; // a fake button inside it
const PREVIEWS = {
	// arrows & pointers
	'arrow-default': ['draw', `--rect=${BTN}`, '--text=Click here'],
	'arrow-ring': ['draw', `--rect=${BTN}`, '--style=ring', '--text=This one'],
	'arrow-box': ['draw', '--rect=640,420,640,180', '--style=box', '--text=Fill these in', '--color=green'],
	'arrow-shape-bend': ['draw', `--rect=${BTN}`, '--shape=bend', '--text=bend'],
	'arrow-shape-straight': ['draw', `--rect=${BTN}`, '--shape=straight', '--text=straight'],
	'arrow-shape-zigzag': ['draw', `--rect=${BTN}`, '--shape=zigzag', '--text=zigzag'],
	'arrow-shape-spiral': ['draw', `--rect=${BTN}`, '--shape=spiral', '--text=spiral'],
	'arrow-size-S': ['draw', `--rect=${BTN}`, '--size=S', '--text=size S'],
	'arrow-size-M': ['draw', `--rect=${BTN}`, '--size=M', '--text=size M'],
	'arrow-size-L': ['draw', `--rect=${BTN}`, '--size=L', '--text=size L'],
	'arrow-color-amber': ['draw', `--rect=${BTN}`, '--color=amber', '--text=amber'],
	'arrow-color-hex': ['draw', `--rect=${BTN}`, '--color=#ff2d55', '--text=#ff2d55'],
	'arrow-from-top': ['draw', `--rect=${BTN}`, '--from=top', '--text=from the top'],
	'arrow-at': ['draw', '--at=960,500', '--text=Here', '--shape=straight'],
	// text labels
	...Object.fromEntries(['bottom-center', 'top-center', 'center', 'left', 'right', 'top-left', 'top-right', 'bottom-left', 'bottom-right'].map((p) => [`text-pos-${p}`, ['draw', 'text', p, `--rect=${WIN}`, `--position=${p}`]])),
	'text-size-S': ['draw', 'text', 'size S', `--rect=${WIN}`, '--position=center', '--size=S'],
	'text-size-M': ['draw', 'text', 'size M', `--rect=${WIN}`, '--position=center', '--size=M'],
	'text-size-L': ['draw', 'text', 'size L', `--rect=${WIN}`, '--position=center', '--size=L'],
	'text-size-XL': ['draw', 'text', 'size XL', `--rect=${WIN}`, '--position=center', '--size=XL'],
	'text-color': ['draw', 'text', 'Done ✓', `--rect=${WIN}`, '--position=center', '--size=XL', '--color=green'],
	'text-bg-none': ['draw', 'text', 'no background', `--rect=${WIN}`, '--position=center', '--size=L', '--bg=none'],
	'text-bg-color': ['draw', 'text', 'custom background', `--rect=${WIN}`, '--position=center', '--size=L', '--bg=#1b1c33', '--color=#c3c0ff'],
	'text-bold': ['draw', 'text', 'bold weight', `--rect=${WIN}`, '--position=center', '--size=L', '--weight=bold'],
	'text-font': ['draw', 'text', 'Avenir Next Condensed', `--rect=${WIN}`, '--position=center', '--size=L', '--font=Avenir Next Condensed'],
	'text-multiline': ['draw', 'text', 'Line one\\nLine two', `--rect=${WIN}`, '--position=bottom-right', '--bg=none'],
	'text-margin': ['draw', 'text', 'margin 10%', `--rect=${WIN}`, '--position=top-left', '--margin=10%'],
	'text-maxwidth': ['draw', 'text', 'A longer label that wraps because max-width is set to forty percent of the window', `--rect=${WIN}`, '--position=center', '--max-width=40%', '--align=left'],
	'text-default': ['draw', 'text', 'Your notes live here', '--rect=0,0,1920,1080'],
	// subtitles
	'subtitle-default': ['subtitle', 'show', 'Opening the export dialog…', '--rect=0,0,1920,1080'],
	'subtitle-top': ['subtitle', 'show', 'running the migration', '--rect=0,0,1920,1080', '--position=top-center'],
	'subtitle-xl': ['subtitle', 'show', 'Done ✓', '--rect=0,0,1920,1080', '--size=XL', '--color=green'],
	'subtitle-bg-none': ['subtitle', 'show', 'no pill behind the text', '--rect=0,0,1920,1080', '--bg=none'],
	'subtitle-over-window': ['subtitle', 'show', "Now I'm opening this window", `--rect=${WIN}`, '--position=bottom-center'],
	'subtitle-left': ['subtitle', 'show', 'step 1 of 3', '--rect=0,0,1920,1080', '--position=left', '--size=L', '--weight=bold'],
};

function desktopSvg() {
	return `<svg xmlns="http://www.w3.org/2000/svg" width="1920" height="1080">
  <defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#2b2f4a"/><stop offset="1" stop-color="#101320"/></linearGradient></defs>
  <rect width="1920" height="1080" fill="url(#g)"/>
  <rect width="1920" height="36" fill="#0b0c0fcc"/>
  <circle cx="22" cy="18" r="6" fill="#ffffff66"/><rect x="44" y="12" width="60" height="12" rx="6" fill="#ffffff55"/><rect x="118" y="12" width="40" height="12" rx="6" fill="#ffffff33"/><rect x="172" y="12" width="40" height="12" rx="6" fill="#ffffff33"/>
  <g transform="translate(560,240)">
    <rect width="800" height="520" rx="14" fill="#f4f4f6"/>
    <rect width="800" height="44" rx="14" fill="#e6e6ea"/><rect y="30" width="800" height="14" fill="#e6e6ea"/>
    <circle cx="22" cy="22" r="7" fill="#ff5f57"/><circle cx="44" cy="22" r="7" fill="#febc2e"/><circle cx="66" cy="22" r="7" fill="#28c840"/>
    <rect x="30" y="80" width="520" height="16" rx="8" fill="#c9c9cf"/>
    <rect x="30" y="116" width="420" height="16" rx="8" fill="#d9d9de"/>
    <rect x="30" y="180" width="740" height="40" rx="8" fill="#ffffff" stroke="#d0d0d6"/>
    <rect x="30" y="236" width="740" height="40" rx="8" fill="#ffffff" stroke="#d0d0d6"/>
    <rect x="30" y="292" width="740" height="40" rx="8" fill="#ffffff" stroke="#d0d0d6"/>
    <rect x="300" y="336" width="200" height="56" rx="10" fill="#4f46e5"/>
    <rect x="360" y="358" width="80" height="12" rx="6" fill="#ffffffcc"/>
    <rect x="520" y="336" width="140" height="56" rx="10" fill="#e6e6ea"/>
  </g>
</svg>`;
}

async function previews() {
	const repoRoot = resolve(site, '..');
	const bin = join(work, 'aos');
	execFileSync('go', ['build', '-o', bin, './cmd/aos'], { cwd: repoRoot, stdio: 'inherit' });
	const bg = await sharp(Buffer.from(desktopSvg())).png().toBuffer();
	const dir = join(site, 'public', 'shots');
	mkdirSync(join(dir, 'full'), { recursive: true });
	for (const [name, args] of Object.entries(PREVIEWS)) {
		const png = join(work, `${name}.png`);
		execFileSync(bin, [...args, `--preview=${png}`], { stdio: 'ignore' });
		const composed = await sharp(bg).composite([{ input: png }]).png().toBuffer();
		await sharp(composed).resize({ width: 640 }).webp({ quality: 78 }).toFile(join(dir, `${name}.webp`));
		await sharp(composed).webp({ quality: 82 }).toFile(join(dir, 'full', `${name}.webp`));
		console.log(`images: shots/${name} from --preview`);
	}
}

const cards = join(site, 'public', 'cards');
const shots = join(site, 'public', 'shots');
for (const [n, s] of Object.entries(CARD_STILLS)) await still(n, s, cards);
for (const [n, t] of Object.entries(T)) await terminal(n, t, cards);
for (const [n, s] of Object.entries(SHOT_STILLS)) await still(n, s, shots);
for (const [n, t] of Object.entries(SHOT_TERMINALS)) await terminal(n, t, shots);
await previews();
rmSync(work, { recursive: true, force: true });
