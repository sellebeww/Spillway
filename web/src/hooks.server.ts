// Copyright (c) 2026 Vassel Goleyu. MIT License.
import { TILE_URL } from '$app/env/private';
import { redirect } from '@sveltejs/kit';
import type { Handle } from '@sveltejs/kit/hooks';
import { publicApiBase, sessionCookieName, sessions } from '#lib/server/auth.ts';
import { originOf, withExtraOrigins } from '#lib/server/security.ts';

export const handle: Handle = async ({ event, resolve }) => {
	event.locals.session = sessions.get(event.cookies.get(sessionCookieName(event.url)));

	const path = event.url.pathname;
	const isAdmin = path === '/admin' || path.startsWith('/admin/');
	if (isAdmin && !event.locals.session) {
		redirect(303, `/auth/login?next=${encodeURIComponent(path + event.url.search)}`);
	}

	const response = await resolve(event);

	const h = response.headers;
	const csp = h.get('content-security-policy');
	if (csp) {
		const api = originOf(publicApiBase());
		const tiles = originOf(TILE_URL);
		h.set(
			'content-security-policy',
			withExtraOrigins(csp, { 'connect-src': [api, tiles], 'img-src': [tiles] })
		);
	}
	h.set('x-content-type-options', 'nosniff');
	h.set('x-frame-options', 'DENY');
	// Tile servers such as OSM require a Referer; send the origin only, and nothing on downgrade.
	h.set('referrer-policy', 'strict-origin-when-cross-origin');
	h.set('permissions-policy', 'camera=(), microphone=(), geolocation=(), payment=()');
	h.set('cross-origin-opener-policy', 'same-origin');
	if (isAdmin || path.startsWith('/auth/')) h.set('cache-control', 'no-store');
	return response;
};
