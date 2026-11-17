// Copyright (c) 2026 Vassel Goleyu. MIT License.
import { error, redirect } from '@sveltejs/kit';
import { messages } from '#lib/messages.ts';
import {
	cookieOptions,
	discovery,
	oidcConfig,
	pending,
	PENDING_COOKIE,
	sessionCookieName,
	sessions
} from '#lib/server/auth.ts';
import { exchangeCode, verifyIdToken } from '#lib/server/oidc.ts';
import type { RequestHandler } from './$types';

/** Finishes the flow: checks state, redeems the code with the PKCE verifier, verifies the ID token, opens a session. */
export const GET: RequestHandler = async ({ url, cookies }) => {
	const cfg = oidcConfig(url);
	if (!cfg) error(503, messages.auth.unavailable);

	const state = url.searchParams.get('state');
	const code = url.searchParams.get('code');
	const boundState = cookies.get(PENDING_COOKIE);
	cookies.delete(PENDING_COOKIE, { path: '/' });
	// The state must match both the cookie of this browser and a login we started (single use).
	const p = state && state === boundState ? pending.take(state) : undefined;
	if (!p || !code || url.searchParams.get('error')) error(400, messages.auth.stateMismatch);

	let session;
	try {
		const disc = await discovery(cfg);
		const tokens = await exchangeCode(disc, cfg, code, p.verifier);
		if (!tokens.idToken) throw new Error('no ID token');
		const claims = await verifyIdToken(tokens.idToken, disc, {
			issuer: cfg.issuer,
			clientId: cfg.clientId,
			nonce: p.nonce,
			nowMs: Date.now()
		});
		// A fresh id on every login: a session id planted before login is never reused.
		sessions.delete(cookies.get(sessionCookieName(url)));
		session = sessions.create({
			accessToken: tokens.accessToken,
			refreshToken: tokens.refreshToken,
			idToken: tokens.idToken,
			expiresAt: Date.now() + tokens.expiresInS * 1000,
			label: claims.email ?? claims.preferred_username ?? claims.name
		});
	} catch {
		error(502, messages.auth.failed);
	}
	cookies.set(sessionCookieName(url), session.id, cookieOptions(url, 12 * 3600));
	redirect(303, p.next);
};
