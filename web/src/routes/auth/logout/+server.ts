// Copyright (c) 2026 Vassel Goleyu. MIT License.
import { redirect } from '@sveltejs/kit';
import { discovery, oidcConfig, sessionCookieName, sessions } from '#lib/server/auth.ts';
import { endSessionUrl } from '#lib/server/oidc.ts';
import type { RequestHandler } from './$types';

/** POST only (SvelteKit's origin check protects it from cross-site forms). Ends the local and the provider session. */
export const POST: RequestHandler = async ({ url, cookies, locals }) => {
	const idToken = locals.session?.idToken;
	sessions.delete(cookies.get(sessionCookieName(url)));
	cookies.delete(sessionCookieName(url), { path: '/' });
	const cfg = oidcConfig(url);
	let out: string | null = null;
	if (cfg) {
		try {
			out = endSessionUrl(await discovery(cfg), idToken, `${url.origin}/`);
		} catch {
			/* provider unreachable: the local session is already gone */
		}
	}
	if (out && cfg) redirect(303, out, { external: [new URL(cfg.issuer).origin] });
	redirect(303, '/');
};
