// Copyright (c) 2026 Vassel Goleyu. MIT License.
// Runtime configuration, read from the environment when the server starts (see .env.example).
import { defineEnvVars } from '@sveltejs/kit/env';

function text(fallback: string): (value: string | undefined) => string;
function text(): (value: string | undefined) => string | undefined;
function text(fallback?: string) {
	return (value: string | undefined) => value?.trim() || fallback;
}

export const variables = defineEnvVars({
	API_URL: {
		schema: text('http://127.0.0.1:8080'),
		description: 'Spillway API, as the web server reaches it'
	},
	PUBLIC_API_URL: {
		schema: text(),
		description:
			'Spillway API as browsers reach it (live stream, chart ranges). Defaults to API_URL'
	},
	TILE_URL: { schema: text(), description: 'Raster basemap template with {z}/{x}/{y}' },
	TILE_ATTRIBUTION: { schema: text(), description: 'Attribution text for the basemap' },
	OIDC_ISSUER: { schema: text(), description: 'Keycloak realm URL. Empty disables sign-in' },
	OIDC_CLIENT_ID: { schema: text('spillway-web'), description: 'Public OIDC client (PKCE)' }
});
