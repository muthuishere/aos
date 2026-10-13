// Renders each showcase panel (src/data/showcase.json) as a social card PNG
// into public/social/: <id>-1200x1200.png and <id>-1200x630.png. The card is
// the same HTML and CSS the landing page uses for the panel, so what gets
// posted is what the site shows.
//
//   node scripts/make-social.mjs
// Needs Google Chrome.

import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const site = resolve(here, '..');
const data = JSON.parse(readFileSync(join(site, 'src', 'data', 'showcase.json'), 'utf8'));
const out = join(site, 'public', 'social');
const work = join(tmpdir(), 'aos-social');
mkdirSync(out, { recursive: true });
mkdirSync(work, { recursive: true });
const CHROME = process.env.CHROME || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';

const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

// Keep in step with .panel styles in src/pages/index.astro.
const css = `
*{box-sizing:border-box}
html,body{margin:0;background:#0b0c0f;color:#f2f3f5;font:16px/1.5 Inter,-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif}
.card{width:var(--w);height:var(--h);padding:var(--pad);display:flex;flex-direction:column;gap:var(--gap);background:radial-gradient(1200px 600px at 20% -10%,#1b1c33 0%,#0b0c0f 60%)}
.brand{display:flex;align-items:baseline;gap:14px}
.mark{font-weight:800;font-size:40px;letter-spacing:-.05em;background:linear-gradient(120deg,#f2f3f5 30%,#8b8bff);-webkit-background-clip:text;background-clip:text;color:transparent}
.sub{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:13px;text-transform:uppercase;letter-spacing:.18em;color:#9aa0a8}
h1{margin:0;font-size:var(--t);line-height:1.12;letter-spacing:-.03em;max-width:26ch}
.rows{display:flex;flex-direction:column;gap:var(--rg);flex:1;justify-content:center;min-height:0}
.row{background:#07080a;border:1px solid #23262d;border-radius:14px;padding:var(--rp);font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:var(--m);line-height:1.5;white-space:pre-wrap;overflow-wrap:anywhere}
.row b{color:#8b85ff;font-weight:600}.row .c{color:#f2f3f5}.row .o{color:#9aa0a8;display:block}
.foot{color:#9aa0a8;font-size:15px;border-top:1px solid #23262d;padding-top:18px}
`;

function rows(p) {
	return p.rows
		.map((r) => `<div class="row"><b>$</b> <span class="c">${esc(r.cmd)}</span>${r.out ? `<span class="o">${esc(r.out)}</span>` : ''}</div>`)
		.join('');
}

function html(p, w, h) {
	const square = h >= w;
	const v = square
		? { t: '44px', m: '19px', pad: '56px 64px', gap: '28px', rp: '16px 20px', rg: '14px' }
		: { t: '32px', m: '15px', pad: '36px 56px', gap: '16px', rp: '10px 16px', rg: '10px' };
	return `<!doctype html><meta charset="utf-8"><style>${css}:root{--w:${w}px;--h:${h}px;--t:${v.t};--m:${v.m};--pad:${v.pad};--gap:${v.gap};--rp:${v.rp};--rg:${v.rg}}</style>
<div class="card"><div class="brand"><span class="mark">aos</span><span class="sub">hands for your AI agent</span></div>
<h1>${esc(p.title)}</h1><div class="rows">${rows(p)}</div><div class="foot">${esc(data.footer)}</div></div>`;
}

for (const p of data.panels) {
	for (const [w, h] of [[1200, 1200], [1200, 630]]) {
		const f = join(work, `${p.id}-${w}x${h}.html`);
		writeFileSync(f, html(p, w, h));
		const png = join(out, `${p.id}-${w}x${h}.png`);
		execFileSync(CHROME, ['--headless=new', '--hide-scrollbars', '--force-device-scale-factor=1', `--screenshot=${png}`, `--window-size=${w},${h}`, `file://${f}`], { stdio: 'ignore' });
		console.log(`social: ${p.id}-${w}x${h}.png`);
	}
}
rmSync(work, { recursive: true, force: true });
