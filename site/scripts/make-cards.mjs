// Builds the "What it does" card thumbnails in public/cards/.
//
// Two sources:
//   - stills from public/demo/aos-demo.mp4 (ffmpeg), one per capability the
//     demo narrates;
//   - terminal-style renders of real command output (headless Chrome), for
//     capabilities the demo does not show. The text in TERMINALS below is the
//     verbatim output of the command, with the machine's own paths, address
//     and network name replaced.
//
// Run by hand when the demo or the output changes:
//   node scripts/make-cards.mjs
// Needs ffmpeg and Google Chrome. Output is 640px-wide webp.

import { execFileSync } from 'node:child_process';
import { mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import sharp from 'sharp';

const here = dirname(fileURLToPath(import.meta.url));
const site = resolve(here, '..');
const video = join(site, 'public', 'demo', 'aos-demo.mp4');
const outDir = join(site, 'public', 'cards');
const work = join(tmpdir(), 'aos-cards');
mkdirSync(outDir, { recursive: true });
mkdirSync(work, { recursive: true });

const CHROME =
	process.env.CHROME ||
	'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';

// name -> second in the demo
const STILLS = {
	windows: 35,
	launch: 40,
	find: 55,
	typing: 70,
	labels: 90,
	point: 105,
	video: 120,
};

// name -> [prompt line, output lines]
const TERMINALS = {
	screenshot: [
		'aos capture screenshot --app=Finder --out=shot.png',
		['/Users/you/shot.png  1730x933+2045+94'],
	],
	shell: [
		'aos exec capture -- git status --short',
		['{', '  "command": ["git", "status", "--short"],', '  "exit": 0,', '  "stdout": "",', '  "stderr": "",', '  "duration_ms": 14', '}'],
	],
	watch: [
		'aos watch clipboard --max=1',
		['{"event":"clipboard","at":"2026-10-13T06:36:30+05:30",', ' "seq":1,"length":14,"digest":"4b559aab5a29"}'],
	],
	clipboard: [
		'aos clipboard copy "hello from aos" && aos clipboard paste',
		['hello from aos'],
	],
	process: [
		'aos process find Finder',
		['   2424    8.0      108.9MB  you  Finder', '', '$ aos service list', 'no services'],
	],
	files: [
		'aos file read site/package.json --lines=1:6',
		['{', '  "name": "aos-site",', '  "type": "module",', '  "version": "0.0.1",', '  "scripts": {', '    "dev": "npm run gen && astro dev",'],
	],
	system: [
		'aos audio volume',
		['82', '', '$ aos battery status', '100%  charged', '', '$ aos network status', 'interface  en0', 'address    192.168.1.24', 'wifi       HomeNet'],
	],
	audit: [
		'aos obs stats --since=24h',
		[
			'route                calls  errors   p50ms   p95ms',
			'key press              157      20      81     313',
			'window list            139      11      25      44',
			'window move            125      52     121     201',
			'key type                94       5     306    1273',
			'capture screenshot      66       0     117     137',
			'draw                    63       8     226   13247',
		],
	],
	adapters: [
		'aos adapters example',
		['{', '  "group": "my",', '  "description": "My own commands",', '  "commands": [', '    {', '      "name": "hello",', '      "summary": "Say hello, to prove adapters work",', '      "args": "[name]",', '      "run": "echo hello"', '    }'],
	],
	remote: [
		'aos remote --help',
		['remote — Hand this desktop\'s screen and input', '         to a browser on the LAN', '', 'Commands:', '  aos remote share [--monitor=<n>] [--port=<n>]', '                   [--fps=<n>] [--key=<k>]', '  aos remote url'],
	],
	mcp: [
		'aos serve tools --groups=window',
		[
			'window_arrange   Apply a saved layout of many windows',
			'window_focus     Bring a window to the front',
			'window_list      List open windows',
			'window_move      Move a window to a zone, a split, or bounds',
			'window_resize    Resize a window in place',
			'window_save      Save the current layout to a file',
		],
	],
};

const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

function terminalHtml(prompt, lines) {
	const body = lines.map((l) => (l.startsWith('$ ') ? `<b>$</b> ${esc(l.slice(2))}` : esc(l))).join('\n');
	return `<!doctype html><meta charset="utf-8"><style>
html,body{margin:0;background:#07080a}
.t{width:960px;height:540px;box-sizing:border-box;padding:22px 28px;background:#07080a;color:#e6e8ec;
font:34px/1.45 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;position:relative}
.bar{display:flex;gap:12px;margin-bottom:22px}.bar i{width:20px;height:20px;border-radius:50%;background:#ffffff26}
pre{margin:0;white-space:pre-wrap;overflow-wrap:anywhere}
b{color:#8b85ff;font-weight:600}.p{color:#f2f3f5}
.fade{position:absolute;left:0;right:0;bottom:0;height:120px;background:linear-gradient(#07080a00,#07080a)}
</style><div class="t"><div class="bar"><i></i><i></i><i></i></div>
<pre><b>$</b> <span class="p">${esc(prompt)}</span>
${body}</pre><div class="fade"></div></div>`;
}

async function toWebp(png, name) {
	await sharp(png).resize({ width: 640 }).webp({ quality: 78 }).toFile(join(outDir, `${name}.webp`));
}

for (const [name, sec] of Object.entries(STILLS)) {
	const png = join(work, `${name}.png`);
	execFileSync('ffmpeg', ['-v', 'error', '-y', '-ss', String(sec), '-i', video, '-frames:v', '1', png]);
	await toWebp(png, name);
	console.log(`cards: ${name}.webp from demo @ ${sec}s`);
}

for (const [name, [prompt, lines]] of Object.entries(TERMINALS)) {
	const html = join(work, `${name}.html`);
	const png = join(work, `${name}.png`);
	writeFileSync(html, terminalHtml(prompt, lines));
	execFileSync(CHROME, [
		'--headless=new', '--hide-scrollbars', '--force-device-scale-factor=1',
		`--screenshot=${png}`, '--window-size=960,540', `file://${html}`,
	], { stdio: 'ignore' });
	await toWebp(png, name);
	console.log(`cards: ${name}.webp rendered from terminal output`);
}
rmSync(work, { recursive: true, force: true });
