// Copyright (c) 2026 Vassel Goleyu. MIT License.
// Glue between SvelteKit (cookies, env) and the OIDC and API clients.

import { API_URL, OIDC_CLIENT_ID, OIDC_ISSUER, PUBLIC_API_URL } from '$app/env/private';
import type { RequestEvent } from '@sveltejs/kit';
import { apiRequest, type ApiResult } from './api';
import { discover, refreshTokens, type Discovery, type OidcConfig } from './oidc';
import { PendingStore, SessionStore, type Session } from './session';

export const sessions = new SessionStore();
export const pending = new PendingStore();

export const PENDING_COOKIE = 'spillway_oidc';

const secure = (url: URL) => url.protocol === 'https:';

/** "__Host-" binds the cookie to this exact host over https (no Domain, Path=/). */
export const sessionCookieName = (url: URL): string =>
	secure(url) ? '__Host-spillway_sid' : 'spillway_sid';

export function cookieOptions(url: URL, maxAgeS: number) {
	return {
		path: '/',
		httpOnly: true,
		secure: secure(url),
		sameSite: 'lax' as const,
		maxAge: maxAgeS
	};
}

export const apiBase = (): string => API_URL;

/** Where the BROWSER reaches the public API (live stream and chart ranges). Same-origin when empty. */
export const publicApiBase = (): string => PUBLIC_API_URL ?? API_URL;

export function oidcConfig(url: URL): OidcConfig | null {
	if (!OIDC_ISSUER) return null;
	return {
		issuer: OIDC_ISSUER,
		clientId: OIDC_CLIENT_ID ?? 'spillway-web',
		redirectUri: `${url.origin}/auth/callback`,
		scope: 'openid profile email'
	};
}

let cached: { at: number; doc: Discovery } | null = null;

export async function discovery(cfg: OidcConfig): Promise<Discovery> {
	if (cached && Date.now() - cached.at < 3_600_000) return cached.doc;
	const doc = await discover(cfg.issuer);
	cached = { at: Date.now(), doc };
	return doc;
}

const refreshing = new Map<string, Promise<boolean>>();

/** Refreshes the access token shortly before it expires. False means the session is over. */
async function ensureFresh(s: Session, url: URL): Promise<boolean> {
	if (s.expiresAt - Date.now() > 30_000) return true;
	const cfg = oidcConfig(url);
	if (!cfg || !s.refreshToken) {
		sessions.delete(s.id);
		return false;
	}
	let p = refreshing.get(s.id);
	if (!p) {
		p = (async () => {
			try {
				const t = await refreshTokens(await discovery(cfg), cfg, s.refreshToken as string);
				s.accessToken = t.accessToken;
				s.refreshToken = t.refreshToken ?? s.refreshToken;
				s.idToken = t.idToken ?? s.idToken;
				s.expiresAt = Date.now() + t.expiresInS * 1000;
				return true;
			} catch {
				sessions.delete(s.id);
				return false;
			} finally {
				refreshing.delete(s.id);
			}
		})();
		refreshing.set(s.id, p);
	}
	return p;
}

function clientIp(event: RequestEvent): string | undefined {
	try {
		return event.getClientAddress();
	} catch {
		return undefined;
	}
}

/** Calls to the public API (no token). The visitor's address is forwarded so rate limits stay per visitor. */
export function publicApi(event: RequestEvent) {
	return <T>(path: string): Promise<ApiResult<T>> =>
		apiRequest<T>({ base: apiBase(), path, clientIp: clientIp(event), timeoutMs: 5000 });
}

export type AdminApi = <T>(
	path: string,
	init?: { method?: 'GET' | 'POST' | 'PUT'; body?: unknown }
) => Promise<ApiResult<T>>;

/**
 * Calls to the authenticated API. The bearer token is attached here and never leaves the server.
 * A 401 means the token was rejected: the session is dropped and the caller should send the user to sign in.
 */
export function adminApi(event: RequestEvent): AdminApi {
	return async <T>(
		path: string,
		init: { method?: 'GET' | 'POST' | 'PUT'; body?: unknown } = {}
	) => {
		const s = event.locals.session;
		if (!s || !(await ensureFresh(s, event.url))) {
			return {
				ok: false,
				status: 401,
				code: 'unauthorized',
				message: 'signed out'
			} satisfies ApiResult<T>;
		}
		const r = await apiRequest<T>({
			base: apiBase(),
			path,
			token: s.accessToken,
			clientIp: clientIp(event),
			...init
		});
		if (!r.ok && r.status === 401) sessions.delete(s.id);
		return r;
	};
}
