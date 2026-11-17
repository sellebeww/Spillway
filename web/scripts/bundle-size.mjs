// Copyright (c) 2026 Vassel Goleyu. MIT License.
// Reports the client JS/CSS an initial page load downloads (static import closure, gzip) and the lazy chunks.
// Run after `npm run build`: node scripts/bundle-size.mjs
import { readFileSync, readdirSync } from 'node:fs';
import { gzipSync } from 'node:zlib';
import { join } from 'node:path';

const root = '.svelte-kit/output/client';
const manifest = JSON.parse(readFileSync(join(root, '.vite/manifest.json'), 'utf8'));
const gz = (f) => gzipSync(readFileSync(join(root, f))).length;
const kb = (n) => `${(n / 1024).toFixed(1)} kB`;

function closure(keys) {
	const seen = new Set();
	const walk = (k) => {
		const e = manifest[k];
		if (!e || seen.has(k)) return;
		seen.add(k);
		(e.imports ?? []).forEach(walk);
		(e.css ?? []).forEach((c) => seen.add(`css:${c}`));
	};
	keys.forEach(walk);
	return [...seen].map((k) => (k.startsWith('css:') ? k.slice(4) : manifest[k].file));
}
const size = (files) => files.reduce((a, f) => a + gz(f), 0);

const keyOf = (re) => Object.keys(manifest).filter((k) => re.test(k));
const base = keyOf(/client-optimized\/app\.js|runtime\/client\/entry\.js|nodes\/(0|1)\.js$/);
const baseFiles = closure(base);
console.log(`Shared base (entry, app, root layout, error page): ${kb(size(baseFiles))} gzip`);

const dir = '.svelte-kit/generated/build/client-optimized/nodes';
for (const f of readdirSync(dir).sort((a, b) => parseInt(a) - parseInt(b))) {
	const src = readFileSync(join(dir, f), 'utf8');
	const page = /routes\/([^'"]*\+page\.svelte)/.exec(src)?.[1];
	if (!page) continue;
	const files = closure([...base, `.svelte-kit/generated/build/client-optimized/nodes/${f}`]);
	console.log(
		`  /${page.replace('+page.svelte', '').replace(/\/$/, '') || ''}`.padEnd(30),
		kb(size(files)),
		'gzip initial JS+CSS'
	);
}
const lazy = [
	['MapLibre (map and site editor only)', 'node_modules/maplibre-gl/dist/maplibre-gl.mjs'],
	['MapLibre CSS', 'node_modules/maplibre-gl/dist/maplibre-gl.css'],
	['ECharts, modular (site page only)', 'src/lib/echarts.ts']
];
for (const [name, key] of lazy)
	if (manifest[key]) console.log(`Lazy: ${name}`.padEnd(52), kb(gz(manifest[key].file)), 'gzip');
