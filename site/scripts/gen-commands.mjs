// Generates the command reference under src/content/docs/reference/ from the
// binary itself, plus src/data/registry.json for every page that quotes a count.
//
// It shells out to `go run ./cmd/aos commands --json --all` in the repo root
// (and `aos --help` for the one-line group descriptions), so the reference
// cannot drift from what the CLI registers: a new command, a renamed one or a
// new example shows up on the next site build. `--all` is used deliberately:
// the reference documents the whole registry, not just what this build
// machine happens to support.
//
// Output:
//   reference/commands.mdx   index: one card per group
//   reference/<group>.mdx    one page per group, one section per command
//
// Wired into `npm run build` via the `prebuild` script.

import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, '..', '..');
const refDir = resolve(here, '..', 'src', 'content', 'docs', 'reference');
const dataOut = resolve(here, '..', 'src', 'data', 'registry.json');
// Hand-written introductions, one Markdown file per group, kept outside the
// generated tree so a regeneration never wipes them. A group without one gets
// only the generated lead line.
const introDir = resolve(here, '..', 'src', 'intros');

// Find the main package rather than hard-coding its name: the command has been
// renamed once already, and a docs generator that breaks on a rename is a
// generator that will quietly be replaced by a hand-written page.
function mainPackage() {
	const cmdDir = join(repoRoot, 'cmd');
	const found = readdirSync(cmdDir, { withFileTypes: true })
		.filter((d) => d.isDirectory() && existsSync(join(cmdDir, d.name, 'main.go')))
		.map((d) => `./cmd/${d.name}`);
	if (found.length !== 1) {
		console.error(
			`gen-commands: expected exactly one main package under cmd/, found ${found.length}` +
				(found.length ? `: ${found.join(', ')}` : ''),
		);
		process.exit(1);
	}
	return found[0];
}

const pkg = mainPackage();
function run(args) {
	try {
		return execFileSync('go', ['run', pkg, ...args], {
			cwd: repoRoot,
			encoding: 'utf8',
			maxBuffer: 32 * 1024 * 1024,
		});
	} catch (err) {
		console.error(
			`gen-commands: could not run \`go run ${pkg} ${args.join(' ')}\` in ${repoRoot}.\n` +
				'The command reference is generated from the binary, so a Go toolchain is required to build this site.\n' +
				String(err.stderr || err.message),
		);
		process.exit(1);
	}
}

const { commands: registered } = JSON.parse(run(['commands', '--json', '--all']));
// The route strings carry the binary's own name; take it from there rather
// than assuming it.
const bin = registered[0].route.split(' ')[0];

// One line per group, from the top-level help ("window      Desktop windows: …").
const groupDesc = new Map();
{
	const help = run(['--help']);
	const block = help.split(/\nGroups:\n/)[1] || '';
	for (const line of block.split('\n')) {
		const m = line.match(/^\s{2}(\S+)\s{2,}(.+?)\s*$/);
		if (!m) break;
		groupDesc.set(m[1], m[2]);
	}
}

// Only what ships inside the binary. The CLI deliberately discovers adapters
// from the user's config directory and plugins from PATH, which means running
// it on a build machine mixes that machine's private extensions into the
// registry -- and this page is published. Filtering on `source` keeps the
// reference reproducible: it documents the product, not the laptop that built
// it.
const commands = registered.filter((c) => c.source === 'builtin');
const local = registered.length - commands.length;
if (local > 0) {
	console.log(`gen-commands: ignored ${local} adapter/plugin command(s) from this machine`);
}
const visible = commands.filter((c) => !c.hidden);
const hidden = commands.length - visible.length;

const groups = new Map();
for (const c of visible) {
	if (!groups.has(c.group)) groups.set(c.group, []);
	groups.get(c.group).push(c);
}
const names = [...groups.keys()].sort();

// ---------------------------------------------------------------------------
// Usage parsing. The registry carries one usage string per command and no
// per-flag metadata, so the flags table is read off the usage: every `--flag`
// or `--flag=<values>`, whether it sits inside `[...]` (optional) or outside
// (required), and whether it is one arm of an `a | b` choice.
// ---------------------------------------------------------------------------
function parseUsage(usage) {
	const flags = [];
	const positional = [];
	const seen = new Set();
	let depth = 0; // [ ] nesting
	let paren = 0; // ( ) nesting
	let i = 0;
	const n = usage.length;
	const choiceAtParen = new Map();
	while (i < n) {
		const ch = usage[i];
		if (ch === '[') { depth++; i++; continue; }
		if (ch === ']') { depth = Math.max(0, depth - 1); i++; continue; }
		if (ch === '(') { paren++; i++; continue; }
		if (ch === ')') { paren = Math.max(0, paren - 1); i++; continue; }
		if (usage.startsWith('--', i)) {
			let j = i + 2;
			while (j < n && /[A-Za-z0-9-]/.test(usage[j])) j++;
			const name = usage.slice(i, j);
			let values = '';
			if (usage[j] === '=') {
				j++;
				if (usage[j] === '<') {
					const k = usage.indexOf('>', j);
					values = usage.slice(j + 1, k);
					j = k + 1;
				} else {
					let k = j;
					while (k < n && !/[\s\]\)]/.test(usage[k])) k++;
					values = usage.slice(j, k);
					j = k;
				}
			}
			// Is this flag one arm of an `a | b` choice? Look at the
			// surrounding text on the same bracket level.
			const inChoice = choiceContext(usage, i, j);
			if (!seen.has(name)) {
				seen.add(name);
				flags.push({ name, values, optional: depth > 0, choice: inChoice, paren: paren > 0 });
			}
			i = j;
			continue;
		}
		if (ch === '<') {
			const k = usage.indexOf('>', i);
			if (k > i) {
				const name = usage.slice(i + 1, k);
				// `--flag=<x>` was consumed above; a bare <x> here is positional.
				positional.push({ name, optional: depth > 0, choice: choiceContext(usage, i, k + 1) });
				i = k + 1;
				continue;
			}
		}
		i++;
	}
	return { flags, positional };
}

// Reports whether the token at [a, b) is separated from a neighbour by " | "
// at the same bracket depth, i.e. it is one alternative among several.
function choiceContext(usage, a, b) {
	const before = usage.slice(0, a).replace(/\[[^\[\]]*\]/g, (m) => ' '.repeat(m.length));
	const after = usage.slice(b).replace(/\[[^\[\]]*\]/g, (m) => ' '.repeat(m.length));
	return /\|\s*$/.test(before) || /^\s*\|/.test(after);
}

const esc = (s) => String(s).replace(/([<>{}])/g, '\\$1');
const code = (s) => '`' + String(s).replace(/\|/g, '\\|') + '`';

function describeValues(values) {
	if (!values) return '';
	if (values.includes('|')) {
		return values
			.split('|')
			.map((v) => code(v.trim()))
			.join(', ');
	}
	return code(`<${values}>`);
}

function meaning(f) {
	const parts = [];
	parts.push(f.optional ? 'Optional' : f.choice ? 'One of these is required' : 'Required');
	if (f.values && !f.values.includes('|') && /^\d/.test(f.values)) parts.push(`default ${f.values}`);
	if (f.values && /^<?[a-z]+=/.test(f.values) === false && f.values.includes(',')) parts.push('comma-separated');
	return parts.join('. ');
}

function badges(c) {
	const b = [];
	if (c.needs_display) b.push('<span class="cmd-badge gui">needs a display</span>');
	if (c.requires_sudo) b.push('<span class="cmd-badge">needs elevated rights</span>');
	if (c.platforms?.length) b.push(`<span class="cmd-badge">${c.platforms.join(' · ')} only</span>`);
	if (c.aliases?.length) b.push(`<span class="cmd-badge">alias: ${c.aliases.map((a) => `<code>${bin} ${a}</code>`).join(', ')}</span>`);
	return b.length ? `<p class="cmd-badges">${b.join(' ')}</p>` : '';
}

function commandSection(c) {
	const out = [];
	const usage = [c.route, c.args].filter(Boolean).join(' ');
	out.push(`### ${c.route}`);
	out.push('');
	out.push(`<p class="cmd-lead">${esc(c.summary)}</p>`);
	out.push('');
	const b = badges(c);
	if (b) { out.push(b); out.push(''); }
	out.push('```sh');
	out.push(usage);
	out.push('```');
	out.push('');
	const { flags, positional } = parseUsage(c.args || '');
	if (flags.length || positional.length) {
		out.push('| flag | values | meaning |');
		out.push('|---|---|---|');
		for (const p of positional) {
			const req = p.optional ? 'Optional positional argument' : p.choice ? 'One of these is required' : 'Required positional argument';
			out.push(`| ${code(`<${p.name}>`)} | | ${req} |`);
		}
		for (const f of flags) {
			out.push(`| ${code(f.name)} | ${describeValues(f.values)} | ${meaning(f)} |`);
		}
		out.push('');
	}
	if (c.examples?.length) {
		out.push('```sh');
		for (const ex of c.examples) out.push(ex);
		out.push('```');
		out.push('');
	}
	return out.join('\n');
}

// ---------------------------------------------------------------------------
// Write the pages.
// ---------------------------------------------------------------------------
rmSync(refDir, { recursive: true, force: true });
mkdirSync(refDir, { recursive: true });

const surface = `${visible.length} commands across ${names.length} groups`;

// Index.
{
	const L = [];
	L.push('---');
	L.push('title: Every command');
	L.push(`description: The aos command reference, ${surface}, generated from the binary at build time.`);
	L.push('sidebar:');
	L.push('  label: Every command');
	L.push('  order: 0');
	L.push('---');
	L.push('');
	L.push(`**${surface}.** One page per group. Every page is generated from the installed binary, so it cannot describe a command the binary does not have.`);
	L.push('');
	L.push('<div class="group-cards">');
	for (const g of names) {
		const cmds = groups.get(g);
		const gui = cmds.some((c) => c.needs_display);
		const desc = groupDesc.get(g) || cmds[0].summary;
		L.push(`<a class="group-card" href="../${g}/">`);
		L.push(`<strong>${bin} ${g}</strong>`);
		L.push(`<span class="group-count">${cmds.length} command${cmds.length === 1 ? '' : 's'}${gui ? ' · needs a display' : ''}</span>`);
		L.push(`<span class="group-desc">${esc(desc)}</span>`);
		L.push('</a>');
	}
	L.push('</div>');
	L.push('');
	L.push('The same data, machine-readable, comes from the binary you installed:');
	L.push('');
	L.push('```sh');
	L.push(`${bin} commands --json --all`);
	L.push('```');
	L.push('');
	L.push('`needs a display` marks a command that is refused with a reason on a machine with no screen. A platform badge appears only where a command does not exist everywhere; everything else runs on macOS, Windows and Linux.');
	if (hidden) {
		L.push('');
		L.push(`<small class="muted">The binary also registers ${hidden} internal command${hidden === 1 ? '' : 's'} marked hidden. ${hidden === 1 ? 'It is' : 'They are'} not part of the public surface and not listed here.</small>`);
	}
	L.push('');
	writeFileSync(join(refDir, 'commands.mdx'), L.join('\n'));
}

// One page per group.
for (const g of names) {
	const cmds = groups.get(g).sort((a, b) => a.route.localeCompare(b.route));
	const desc = groupDesc.get(g) || cmds[0].summary;
	const L = [];
	L.push('---');
	L.push(`title: ${bin} ${g}`);
	L.push(`description: ${desc.replace(/:/g, ' -')}`);
	L.push('sidebar:');
	L.push(`  label: ${g}`);
	L.push('  order: 1');
	L.push('---');
	L.push('');
	L.push(`<p class="cmd-lead">${esc(desc)}</p>`);
	L.push('');
	L.push(`${cmds.length} command${cmds.length === 1 ? '' : 's'}. Run \`${bin} ${g} --help\` for the same list from the binary, or [see every group](../commands/).`);
	L.push('');
	const intro = join(introDir, `${g}.md`);
	if (existsSync(intro)) {
		L.push(readFileSync(intro, 'utf8').trim());
		L.push('');
		L.push('## Commands');
		L.push('');
	}
	for (const c of cmds) {
		L.push(commandSection(c));
	}
	writeFileSync(join(refDir, `${g}.mdx`), L.join('\n'));
}

// The same counts, for the pages and the landing page to quote, so no page
// has to hard-code a number the registry can change out from under it.
mkdirSync(dirname(dataOut), { recursive: true });
writeFileSync(
	dataOut,
	JSON.stringify(
		{
			// The headline number is what a reader can actually run and find
			// documented. Every page quotes `commands`; `total` includes the
			// hidden internal ones and is only ever mentioned as a footnote.
			commands: visible.length,
			total: commands.length,
			hidden,
			documented: visible.length,
			groups: names.length,
			binary: bin,
			groupList: names.map((g) => {
				const cmds = groups
					.get(g)
					.filter((c) => !c.hidden)
					.sort((a, b) => a.route.localeCompare(b.route));
				return {
					name: g,
					description: groupDesc.get(g) || '',
					count: cmds.length,
					gui: cmds.some((c) => c.needs_display),
					commands: cmds.map((c) => ({
						// The leaf, without the group prefix the chip already shows.
						name: c.route.split(' ').slice(2).join(' '),
						summary: c.summary,
						gui: !!c.needs_display,
						only: (c.platforms || []).length ? c.platforms : undefined,
					})),
				};
			}),
		},
		null,
		2,
	) + '\n',
);
console.log(`gen-commands: wrote ${visible.length} commands in ${names.length} group pages -> ${refDir}`);
