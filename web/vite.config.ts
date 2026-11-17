import { defineConfig } from 'vitest/config';
import adapter from '@sveltejs/adapter-node';
import { sveltekit } from '@sveltejs/kit/vite';

export default defineConfig({
	plugins: [
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},
			adapter: adapter(),
			// Strict base policy; SvelteKit adds nonces for its own inline script. The API and tile origins
			// are only known at run time, so hooks.server.ts appends them (see lib/server/security.ts).
			csp: {
				mode: 'auto',
				directives: {
					'default-src': ['self'],
					'script-src': ['self'],
					// MapLibre and ECharts set inline styles on elements they create.
					'style-src': ['self', 'unsafe-inline'],
					'img-src': ['self', 'data:', 'blob:'],
					'font-src': ['self'],
					'connect-src': ['self'],
					'worker-src': ['self', 'blob:'],
					'child-src': ['blob:'],
					'object-src': ['none'],
					'base-uri': ['self'],
					'frame-ancestors': ['none']
				}
			}
		})
	],
	server: { port: 3000, strictPort: true },
	preview: { port: 4173 },
	test: {
		expect: { requireAssertions: true },
		projects: [
			{
				extends: './vite.config.ts',
				test: {
					name: 'server',
					environment: 'node',
					include: ['src/**/*.{test,spec}.{js,ts}'],
					exclude: ['src/**/*.svelte.{test,spec}.{js,ts}']
				}
			}
		]
	}
});
