import { createHmac, createSign, generateKeyPairSync } from 'node:crypto';
import { describe, expect, test } from 'vitest';
import {
	assertSecureIssuer,
	buildAuthUrl,
	discover,
	pkceChallenge,
	safeNext,
	verifyIdToken,
	type Discovery
} from './oidc';

describe('PKCE', () => {
	test('S256 challenge matches the RFC 7636 appendix B vector', () => {
		expect(pkceChallenge('dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk')).toBe(
			'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM'
		);
	});
});

describe('safeNext (no open redirect)', () => {
	test.each(['/admin/rules?site=1', '/admin/sites', '/'])('keeps %s', (p) => {
		expect(safeNext(p)).toBe(p);
	});
	test.each([
		'https://evil.example/',
		'//evil.example',
		'/\\evil.example',
		'javascript:alert(1)',
		'admin',
		'/a\nb',
		'',
		null,
		undefined,
		'/' + 'a'.repeat(600)
	])('rejects %j', (p) => {
		expect(safeNext(p)).toBe('/admin/sites');
	});
});

describe('issuer transport', () => {
	test('https is fine, plain http only for localhost', () => {
		expect(() => assertSecureIssuer('https://id.example.org/realms/x')).not.toThrow();
		expect(() => assertSecureIssuer('http://localhost:8081/realms/x')).not.toThrow();
		expect(() => assertSecureIssuer('http://127.0.0.1:8081/realms/x')).not.toThrow();
		expect(() => assertSecureIssuer('http://id.example.org/realms/x')).toThrow();
	});
});

const issuer = 'https://id.example.org/realms/spillway';
const disc: Discovery = {
	issuer,
	authorization_endpoint: `${issuer}/auth`,
	token_endpoint: `${issuer}/token`,
	jwks_uri: `${issuer}/certs`
};

describe('authorization URL', () => {
	test('carries PKCE S256, state, nonce and the exact redirect URI', () => {
		const u = buildAuthUrl(
			disc,
			{
				issuer,
				clientId: 'spillway-web',
				redirectUri: 'http://localhost:3000/auth/callback',
				scope: 'openid profile email'
			},
			{ state: 's1', nonce: 'n1', challenge: 'c1' }
		);
		const q = u.searchParams;
		expect(q.get('response_type')).toBe('code');
		expect(q.get('code_challenge_method')).toBe('S256');
		expect(q.get('code_challenge')).toBe('c1');
		expect(q.get('state')).toBe('s1');
		expect(q.get('nonce')).toBe('n1');
		expect(q.get('redirect_uri')).toBe('http://localhost:3000/auth/callback');
	});
});

describe('discovery', () => {
	test('rejects a document whose issuer differs (mix-up)', async () => {
		const f = (async () =>
			new Response(
				JSON.stringify({ ...disc, issuer: 'https://evil.example' })
			)) as unknown as typeof fetch;
		await expect(discover(issuer, f)).rejects.toThrow(/issuer/);
	});
	test('accepts a matching document', async () => {
		const f = (async () => new Response(JSON.stringify(disc))) as unknown as typeof fetch;
		await expect(discover(issuer, f)).resolves.toMatchObject({
			token_endpoint: disc.token_endpoint
		});
	});
});

describe('ID token verification', () => {
	const { privateKey, publicKey } = generateKeyPairSync('rsa', { modulusLength: 2048 });
	const jwk = { ...publicKey.export({ format: 'jwk' }), kid: 'k1', alg: 'RS256', use: 'sig' };
	const other = generateKeyPairSync('rsa', { modulusLength: 2048 });
	const jwks = (async () =>
		new Response(JSON.stringify({ keys: [jwk] }))) as unknown as typeof fetch;
	const now = 1_800_000_000_000;
	const claims = {
		iss: issuer,
		aud: 'spillway-web',
		sub: 'u1',
		nonce: 'n1',
		exp: now / 1000 + 300,
		email: 'a@b.co'
	};
	const exp = { issuer, clientId: 'spillway-web', nonce: 'n1', nowMs: now };
	const enc = (o: unknown) => Buffer.from(JSON.stringify(o)).toString('base64url');

	function sign(payload: object, opts: { alg?: string; key?: typeof privateKey } = {}) {
		const head = enc({ alg: opts.alg ?? 'RS256', kid: 'k1', typ: 'JWT' });
		const body = `${head}.${enc(payload)}`;
		const sig = createSign('RSA-SHA256')
			.update(body)
			.sign(opts.key ?? privateKey)
			.toString('base64url');
		return `${body}.${sig}`;
	}

	test('accepts a valid token', async () => {
		await expect(verifyIdToken(sign(claims), disc, exp, jwks)).resolves.toMatchObject({
			email: 'a@b.co'
		});
	});
	test('accepts an audience array that contains the client', async () => {
		await expect(
			verifyIdToken(sign({ ...claims, aud: ['x', 'spillway-web'] }), disc, exp, jwks)
		).resolves.toBeTruthy();
	});

	test.each([
		['wrong issuer', { ...claims, iss: 'https://evil.example' }],
		['wrong audience', { ...claims, aud: 'other' }],
		['wrong nonce', { ...claims, nonce: 'n2' }],
		['expired', { ...claims, exp: now / 1000 - 3600 }],
		['no exp', { ...claims, exp: undefined }],
		['no subject', { ...claims, sub: '' }],
		['not yet valid', { ...claims, nbf: now / 1000 + 3600 }]
	])('rejects %s', async (_n, payload) => {
		await expect(verifyIdToken(sign(payload), disc, exp, jwks)).rejects.toThrow(
			'ID token rejected'
		);
	});

	test('rejects a signature made with another key', async () => {
		await expect(
			verifyIdToken(sign(claims, { key: other.privateKey }), disc, exp, jwks)
		).rejects.toThrow();
	});

	test('rejects alg none and HS256 key confusion', async () => {
		const none = `${enc({ alg: 'none' })}.${enc(claims)}.`;
		await expect(verifyIdToken(none, disc, exp, jwks)).rejects.toThrow();
		const head = enc({ alg: 'HS256', kid: 'k1' });
		const body = `${head}.${enc(claims)}`;
		const hs = createHmac('sha256', JSON.stringify(jwk)).update(body).digest('base64url');
		await expect(verifyIdToken(`${body}.${hs}`, disc, exp, jwks)).rejects.toThrow();
	});

	test('rejects garbage', async () => {
		for (const t of ['', 'a.b', 'a.b.c', 'x'.repeat(20_000)]) {
			await expect(verifyIdToken(t, disc, exp, jwks)).rejects.toThrow();
		}
	});
});
