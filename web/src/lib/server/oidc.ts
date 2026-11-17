// Copyright (c) 2026 Vassel Goleyu. MIT License.
// Minimal OpenID Connect authorization code flow with PKCE (RFC 7636), server side only.
//
// shortcut: hand-written on node:crypto and fetch because the package registry was unreachable
// when this was built, so openid-client could not be installed or verified. Swap to a maintained
// library (for example openid-client v6) when it can be audited; the surface used by the routes
// is small (discover, buildAuthUrl, exchangeCode, refreshTokens, verifyIdToken).

import { createHash, createPublicKey, randomBytes, verify as rsaVerify } from 'node:crypto';

export interface OidcConfig {
	issuer: string;
	clientId: string;
	redirectUri: string;
	scope: string;
}

export interface Discovery {
	issuer: string;
	authorization_endpoint: string;
	token_endpoint: string;
	jwks_uri: string;
	end_session_endpoint?: string;
}

export interface TokenSet {
	accessToken: string;
	refreshToken?: string;
	idToken?: string;
	expiresInS: number;
}

export interface IdClaims {
	sub: string;
	email?: string;
	preferred_username?: string;
	name?: string;
	[k: string]: unknown;
}

type Fetch = typeof fetch;

export const b64url = (b: Buffer): string => b.toString('base64url');

/** Random URL-safe token, 32 bytes by default (256 bits). */
export function randomToken(bytes = 32): string {
	return b64url(randomBytes(bytes));
}

/** S256 code challenge: BASE64URL(SHA256(verifier)). */
export function pkceChallenge(verifier: string): string {
	return b64url(createHash('sha256').update(verifier).digest());
}

/** Plain http issuers are only acceptable for local development. */
export function assertSecureIssuer(issuer: string): void {
	const u = new URL(issuer);
	const local = ['localhost', '127.0.0.1', '[::1]'].includes(u.hostname);
	if (u.protocol !== 'https:' && !local) {
		throw new Error('OIDC_ISSUER must use https (plain http is allowed for localhost only)');
	}
}

/**
 * Only same-site relative paths are accepted as a post-login target. Anything else (absolute
 * URLs, protocol-relative "//host", backslashes, control characters) falls back to the default.
 */
export function safeNext(next: string | null | undefined, fallback = '/admin/sites'): string {
	if (!next || !next.startsWith('/') || next.startsWith('//') || next.includes('\\'))
		return fallback;
	// eslint-disable-next-line no-control-regex
	if (/[\u0000-\u001f\u007f]/.test(next)) return fallback;
	return next.length > 512 ? fallback : next;
}

async function getJson<T>(fetchFn: Fetch, url: string): Promise<T> {
	const res = await fetchFn(url, { signal: AbortSignal.timeout(10_000), redirect: 'error' });
	if (!res.ok) throw new Error(`${new URL(url).pathname} answered ${res.status}`);
	return (await res.json()) as T;
}

export async function discover(issuer: string, fetchFn: Fetch = fetch): Promise<Discovery> {
	assertSecureIssuer(issuer);
	const doc = await getJson<Discovery>(
		fetchFn,
		`${issuer.replace(/\/$/, '')}/.well-known/openid-configuration`
	);
	// The issuer in the document must be exactly the one we trust (mix-up protection).
	if (doc.issuer !== issuer)
		throw new Error('issuer in the discovery document does not match OIDC_ISSUER');
	for (const k of ['authorization_endpoint', 'token_endpoint', 'jwks_uri'] as const) {
		if (typeof doc[k] !== 'string') throw new Error(`discovery document lacks ${k}`);
	}
	return doc;
}

export function buildAuthUrl(
	disc: Discovery,
	cfg: OidcConfig,
	p: { state: string; nonce: string; challenge: string }
): URL {
	const u = new URL(disc.authorization_endpoint);
	u.searchParams.set('response_type', 'code');
	u.searchParams.set('client_id', cfg.clientId);
	u.searchParams.set('redirect_uri', cfg.redirectUri);
	u.searchParams.set('scope', cfg.scope);
	u.searchParams.set('state', p.state);
	u.searchParams.set('nonce', p.nonce);
	u.searchParams.set('code_challenge', p.challenge);
	u.searchParams.set('code_challenge_method', 'S256');
	return u;
}

async function tokenRequest(
	disc: Discovery,
	form: Record<string, string>,
	fetchFn: Fetch
): Promise<TokenSet> {
	const res = await fetchFn(disc.token_endpoint, {
		method: 'POST',
		headers: { 'content-type': 'application/x-www-form-urlencoded', accept: 'application/json' },
		body: new URLSearchParams(form),
		signal: AbortSignal.timeout(10_000),
		redirect: 'error'
	});
	if (!res.ok) throw new Error(`token endpoint answered ${res.status}`);
	const raw = (await res.json()) as Record<string, unknown>;
	if (typeof raw.access_token !== 'string' || !raw.access_token)
		throw new Error('no access_token in the answer');
	if (typeof raw.token_type === 'string' && raw.token_type.toLowerCase() !== 'bearer') {
		throw new Error('unsupported token_type');
	}
	return {
		accessToken: raw.access_token,
		refreshToken: typeof raw.refresh_token === 'string' ? raw.refresh_token : undefined,
		idToken: typeof raw.id_token === 'string' ? raw.id_token : undefined,
		expiresInS: typeof raw.expires_in === 'number' && raw.expires_in > 0 ? raw.expires_in : 300
	};
}

export function exchangeCode(
	disc: Discovery,
	cfg: OidcConfig,
	code: string,
	verifier: string,
	fetchFn: Fetch = fetch
): Promise<TokenSet> {
	return tokenRequest(
		disc,
		{
			grant_type: 'authorization_code',
			code,
			redirect_uri: cfg.redirectUri,
			client_id: cfg.clientId,
			code_verifier: verifier
		},
		fetchFn
	);
}

export function refreshTokens(
	disc: Discovery,
	cfg: OidcConfig,
	refreshToken: string,
	fetchFn: Fetch = fetch
): Promise<TokenSet> {
	return tokenRequest(
		disc,
		{ grant_type: 'refresh_token', refresh_token: refreshToken, client_id: cfg.clientId },
		fetchFn
	);
}

export function endSessionUrl(
	disc: Discovery,
	idToken: string | undefined,
	postLogout: string
): string | null {
	if (!disc.end_session_endpoint) return null;
	const u = new URL(disc.end_session_endpoint);
	if (idToken) u.searchParams.set('id_token_hint', idToken);
	u.searchParams.set('post_logout_redirect_uri', postLogout);
	return u.toString();
}

interface Jwk {
	kid?: string;
	kty: string;
	[k: string]: unknown;
}

function decodeSegment<T>(seg: string): T {
	return JSON.parse(Buffer.from(seg, 'base64url').toString('utf8')) as T;
}

/**
 * Verifies an ID token: RS256 signature against the provider keys, exact issuer, audience contains
 * the client id, not expired, and the nonce we sent. Throws one generic error on any failure.
 */
export async function verifyIdToken(
	idToken: string,
	disc: Discovery,
	expect: { issuer: string; clientId: string; nonce: string; nowMs: number },
	fetchFn: Fetch = fetch
): Promise<IdClaims> {
	const bad = () => new Error('ID token rejected');
	if (idToken.length > 16_384) throw bad();
	const parts = idToken.split('.');
	if (parts.length !== 3) throw bad();
	let header: { alg?: string; kid?: string };
	let claims: IdClaims & {
		iss?: string;
		aud?: string | string[];
		exp?: number;
		nonce?: string;
		nbf?: number;
	};
	try {
		header = decodeSegment(parts[0]);
		claims = decodeSegment(parts[1]);
	} catch {
		throw bad();
	}
	if (header.alg !== 'RS256') throw bad(); // rejects "none" and HS256 key confusion

	const jwks = await getJson<{ keys: Jwk[] }>(fetchFn, disc.jwks_uri);
	const jwk = jwks.keys.find((k) => k.kty === 'RSA' && (!header.kid || k.kid === header.kid));
	if (!jwk) throw bad();
	let ok: boolean;
	try {
		const key = createPublicKey({ key: jwk as never, format: 'jwk' });
		ok = rsaVerify(
			'RSA-SHA256',
			Buffer.from(`${parts[0]}.${parts[1]}`),
			key,
			Buffer.from(parts[2], 'base64url')
		);
	} catch {
		throw bad();
	}
	if (!ok) throw bad();

	const aud = Array.isArray(claims.aud) ? claims.aud : [claims.aud];
	const leewayS = 30;
	const nowS = expect.nowMs / 1000;
	if (
		claims.iss !== expect.issuer ||
		!aud.includes(expect.clientId) ||
		typeof claims.exp !== 'number' ||
		claims.exp + leewayS < nowS ||
		(typeof claims.nbf === 'number' && claims.nbf - leewayS > nowS) ||
		claims.nonce !== expect.nonce ||
		typeof claims.sub !== 'string' ||
		!claims.sub
	) {
		throw bad();
	}
	return claims;
}
