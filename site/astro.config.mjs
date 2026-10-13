// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightLlmsTxt from 'starlight-llms-txt';
import { readFileSync } from 'node:fs';

// Written by scripts/gen-commands.mjs, which runs the binary. Every count on
// this site comes from here, so no page can quote a number the registry has
// moved past. `npm run build` regenerates it before Astro starts.
const registry = JSON.parse(readFileSync('./src/data/registry.json', 'utf8'));
const surface = `${registry.commands} commands across ${registry.groups} groups`;

// Project GitHub Pages: https://muthuishere.github.io/aos
// The landing page is src/pages/index.astro — a full-viewport canvas, outside
// the Starlight layout on purpose. Starlight owns everything under /docs-ish
// slugs below it.
//
// starlight-sidebar-topics is deliberately NOT used here: it earns its place
// when a site has several parallel trees (toolnexus has one per language). This
// site has one flat set of pages, and a topic picker over a single topic is
// chrome with nothing behind it.
export default defineConfig({
	site: 'https://muthuishere.github.io',
	base: '/aos',
	integrations: [
		starlight({
			title: 'aos',
			description:
				`One CLI over the machine that agents drive the same way you do — as an installed agent skill, or over MCP. ${surface}, on macOS, Windows and Linux.`,
			// The audience for this product is substantially agents, so the
			// llms.txt is load-bearing, not decoration.
			plugins: [
				starlightLlmsTxt({
					projectName: 'aos',
					description:
						`One CLI over the machine that agents drive the same way a person does: ${surface} covering windows, input, screen capture, files, shell commands, packages, network, audio, clipboard and services, on macOS, Windows and Linux.`,
					details:
						'There are two ways in, and the skill is the primary one: `aos skill install` writes an agent skill bundled inside the binary to ~/.claude/skills and ~/.agents/skills, after which an agent that already has a shell simply runs the CLI — nothing listening, nothing to start, and it works over SSH into any machine that has the binary. MCP is the second path, for agents that want typed tools: `aos serve mcp` exposes every non-hidden, non-blocking command as a tool, gated by a token minted per run because loopback is not a permission. Both run the identical code path a person gets at a prompt, so what you test by hand is what the agent gets. Safety is structural: commands that need a screen are refused with a reason on a headless box, `file delete` will not touch a filesystem root or $HOME or a system directory, `serve` binds loopback, services are namespaced, and telemetry records argument counts rather than argument values. One verb set means the same thing everywhere (`pkg install` over brew/winget/scoop/choco/apt/pacman/yay/dnf). It extends without forking through JSON adapters and PATH executables, both of which become MCP tools and neither of which can shadow a built-in.',
				}),
			],
			customCss: ['@fontsource-variable/inter', './src/styles/theme.css'],
			components: { SiteTitle: './src/components/SiteTitle.astro' },
			social: [
				{ icon: 'github', label: 'GitHub', href: 'https://github.com/muthuishere/aos' },
			],
			sidebar: [
				{
					label: 'Start here',
					items: [
						{ label: 'What it is', slug: 'what-it-is' },
						{ label: "What it's not", slug: 'what-it-is-not' },
						{ label: 'Quickstart', slug: 'quickstart' },
						{ label: 'Installing', slug: 'install' },
					],
				},
				{
					label: 'Using it',
					items: [
						{ label: 'The command surface', slug: 'command-surface' },
						{ label: 'Using it from an agent (the skill)', slug: 'agent-skill' },
						{ label: 'Using it from an agent (MCP)', slug: 'mcp' },
						{ label: 'Safety model', slug: 'safety' },
						{ label: 'Headless machines', slug: 'headless' },
						{ label: 'Extending it', slug: 'extending' },
						{ label: 'Watching a machine', slug: 'remote-share' },
						{ label: 'Recording a demo', slug: 'capture-video' },
						{ label: 'Observability', slug: 'observability' },
						{
							label: 'Scenarios: desktop',
							collapsed: true,
							items: [{ autogenerate: { directory: 'scenarios/desktop' } }],
						},
						{
							label: 'Scenarios: guiding and recording',
							collapsed: true,
							items: [{ autogenerate: { directory: 'scenarios/guide' } }],
						},
						{
							label: 'Scenarios: the machine',
							collapsed: true,
							items: [{ autogenerate: { directory: 'scenarios/machine' } }],
						},
						{
							label: 'Scenarios: agents',
							collapsed: true,
							items: [{ autogenerate: { directory: 'scenarios/agents' } }],
						},
					],
				},
				{
					label: 'Drawing on screen',
					items: [
						{ label: 'Arrows & pointers', slug: 'drawing/arrows' },
						{ label: 'Text labels', slug: 'drawing/text' },
						{ label: 'Subtitles', slug: 'drawing/subtitles' },
					],
				},
				{
					label: 'Reference',
					items: [{ autogenerate: { directory: 'reference' } }],
				},
			],
		}),
	],
});
