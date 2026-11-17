// Copyright (c) 2026 Vassel Goleyu. MIT License.
import { error, redirect } from '@sveltejs/kit';
import { messages } from '#lib/messages.ts';
import { cookieOptions, discovery, oidcConfig, pending, PENDING_COOKIE } from '#lib/server/auth.ts';
import { buildAuthUrl, pkceChallenge, randomToken, safeNext } from '#lib/server/oidc.ts';
import type { RequestHandler } from './$types';

/** Starts the authorization code flow with PKCE (S256) and a state bound to this browser. */
export const GET: RequestHandler = async ({ url, cookies }) => {
	const cfg = oidcConfig(url);
	if (!cfg) error(503, messages.auth.unavailable);
	let target: URL;
	try {
		const verifier = randomToken(32);
		const state = randomToken(24);
		const nonce = randomToken(24);
		pending.put(state, { verifier, nonce, next: safeNext(url.searchParams.get('next')) });
		cookies.set(PENDING_COOKIE, state, cookieOptions(url, 600));
		target = buildAuthUrl(await discovery(cfg), cfg, {
			state,
			nonce,
			challenge: pkceChallenge(verifier)
		});
	} catch {
		error(502, messages.auth.unreachable);
	}
	// Only the configured identity provider may be the target of this redirect.
	redirect(303, target.toString(), { external: [new URL(cfg.issuer).origin] });
};
