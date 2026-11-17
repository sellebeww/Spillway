// Copyright (c) 2026 Vassel Goleyu. MIT License.
// A mock Spillway API plus a tiny OIDC provider (real PKCE check, RS256 ID tokens) for the Playwright smoke tests.
import { createHash, createSign, generateKeyPairSync } from 'node:crypto';
import { createServer } from 'node:http';

const PORT = 4010;
const BASE = `http://127.0.0.1:${PORT}`;
const ISSUER = `${BASE}/oidc`;
const TOKEN = 'mock-access-token';
const { privateKey, publicKey } = generateKeyPairSync('rsa', { modulusLength: 2048 });
const jwk = { ...publicKey.export({ format: 'jwk' }), kid: 'mock', alg: 'RS256', use: 'sig' };
const codes = new Map();
const calls = [];
const b64 = (o) => Buffer.from(JSON.stringify(o)).toString('base64url');
const PNG = Buffer.from(
	'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==',
	'base64'
);

const now = () => Date.now();
const iso = (ms) => new Date(ms).toISOString();
const site = (slug, name, lon, lat, level, ageMs, stale, status) => ({
	slug,
	name,
	lon,
	lat,
	level,
	updated: iso(now() - ageMs),
	stale,
	status
});
const SITES = [
	site('kali-putih', 'Kali Putih', 110.25, -7.5, 123.4, 20_000, false, 'ONLINE'),
	site('kali-lama', 'Kali Lama', 110.4, -7.6, 77.7, 3 * 3_600_000, true, 'DELAYED'),
	site('kali-baru', 'Kali Baru', 110.1, -7.4, 55, 30_000, false, 'ONLINE')
];
const props = (s) => ({
	slug: s.slug,
	name: s.name,
	tz: 'Asia/Jakarta',
	level_cm: s.level,
	status: s.status,
	updated_at: s.updated,
	stale: s.stale,
	stale_label: s.stale ? 'DATA TERTUNDA' : null
});
const detail = (s) => ({
	...props(s),
	lat: s.lat,
	lon: s.lon,
	severity: s.slug === 'kali-putih' ? 2 : 0,
	thresholds: [
		{ level_cm: 150, severity: 1 },
		{ level_cm: 200, severity: 2 }
	],
	alerts: [
		{
			severity: 2,
			state: 'CLOSED',
			opened_at: iso(now() - 5 * 3_600_000),
			closed_at: iso(now() - 4 * 3_600_000),
			peak_level_cm: 205
		}
	],
	devices: [
		{
			id: 'd1',
			name: 'Sensor 1',
			level_cm: s.level,
			status: s.status,
			updated_at: s.updated,
			stale: s.stale,
			stale_label: null
		}
	],
	notice: 'BUKAN sistem peringatan dini resmi'
});
const series = (s, url) => {
	const res = url.searchParams.get('res') === 'raw' ? 'raw' : '1m';
	const pts = Array.from({ length: 40 }, (_, i) => {
		const ts = iso(now() - (40 - i) * 60_000 * 2);
		return res === 'raw'
			? { ts, level_cm: s.level - 10 + i * 0.25 }
			: { ts, min: s.level - 12 + i, avg: s.level - 10 + i, max: s.level - 8 + i, n: 4 };
	});
	return { site: s.slug, device_id: 'd1', res, truncated: false, points: pts };
};

function send(res, status, body, headers = {}) {
	res.writeHead(status, {
		'access-control-allow-origin': '*',
		'content-type': 'application/json',
		...headers
	});
	res.end(typeof body === 'string' || Buffer.isBuffer(body) ? body : JSON.stringify(body));
}

const server = createServer(async (req, res) => {
	const url = new URL(req.url, BASE);
	const p = url.pathname;
	const chunks = [];
	for await (const c of req) chunks.push(c);
	const raw = Buffer.concat(chunks).toString();

	if (p === '/__calls') return send(res, 200, calls);
	if (p.startsWith('/tiles/')) return send(res, 200, PNG, { 'content-type': 'image/png' });

	// ---- OIDC ----
	if (p === '/oidc/.well-known/openid-configuration')
		return send(res, 200, {
			issuer: ISSUER,
			authorization_endpoint: `${ISSUER}/auth`,
			token_endpoint: `${ISSUER}/token`,
			jwks_uri: `${ISSUER}/certs`,
			end_session_endpoint: `${ISSUER}/logout`
		});
	if (p === '/oidc/certs') return send(res, 200, { keys: [jwk] });
	if (p === '/oidc/auth') {
		const q = url.searchParams;
		if (q.get('code_challenge_method') !== 'S256' || !q.get('code_challenge'))
			return send(res, 400, 'PKCE required');
		const code = Math.random().toString(36).slice(2);
		codes.set(code, {
			challenge: q.get('code_challenge'),
			nonce: q.get('nonce'),
			client: q.get('client_id')
		});
		const back = new URL(q.get('redirect_uri'));
		back.searchParams.set('code', code);
		back.searchParams.set('state', q.get('state'));
		res.writeHead(302, { location: back.toString() });
		return res.end();
	}
	if (p === '/oidc/token') {
		const f = new URLSearchParams(raw);
		const c = codes.get(f.get('code'));
		codes.delete(f.get('code'));
		const okPkce =
			c &&
			createHash('sha256')
				.update(f.get('code_verifier') ?? '')
				.digest('base64url') === c.challenge;
		if (!okPkce) return send(res, 400, { error: 'invalid_grant' });
		const head = b64({ alg: 'RS256', kid: 'mock', typ: 'JWT' });
		const body = b64({
			iss: ISSUER,
			aud: c.client,
			sub: 'u1',
			nonce: c.nonce,
			email: 'admin@spillway.dev',
			exp: Math.floor(now() / 1000) + 3600
		});
		const sig = createSign('RSA-SHA256')
			.update(`${head}.${body}`)
			.sign(privateKey)
			.toString('base64url');
		return send(res, 200, {
			access_token: TOKEN,
			token_type: 'Bearer',
			expires_in: 3600,
			refresh_token: 'r',
			id_token: `${head}.${body}.${sig}`
		});
	}
	if (p === '/oidc/logout') {
		res.writeHead(302, { location: url.searchParams.get('post_logout_redirect_uri') ?? '/' });
		return res.end();
	}

	// ---- public API ----
	if (p === '/v1/public/sites')
		return send(res, 200, {
			type: 'FeatureCollection',
			features: SITES.map((s) => ({
				type: 'Feature',
				geometry: { type: 'Point', coordinates: [s.lon, s.lat] },
				properties: props(s)
			}))
		});
	let m = /^\/v1\/public\/sites\/([a-z0-9-]+)(\/series)?$/.exec(p);
	if (m) {
		const s = SITES.find((x) => x.slug === m[1]);
		if (!s) return send(res, 404, { error: { code: 'not_found', message: 'not found' } });
		return send(res, 200, m[2] ? series(s, url) : detail(s));
	}
	if (p === '/v1/public/stream') {
		res.writeHead(200, {
			'access-control-allow-origin': '*',
			'content-type': 'text/event-stream',
			'cache-control': 'no-store'
		});
		res.write('retry: 5000\n: ping\n\n');
		return; // stays open; the client closes it
	}

	// ---- authenticated API ----
	if (req.headers.authorization !== `Bearer ${TOKEN}`)
		return send(res, 401, { error: { code: 'unauthorized', message: 'no' } });
	if (p === '/v1/me')
		return send(res, 200, {
			user_id: 'u1',
			email: 'admin@spillway.dev',
			admin: true,
			site_ids: []
		});
	if (p === '/v1/sites')
		return send(res, 200, {
			items: [
				{
					id: 's1',
					slug: 'kali-putih',
					name: 'Kali Putih',
					lat: -7.5,
					lon: 110.25,
					tz: 'Asia/Jakarta',
					is_public: true,
					created_at: iso(now()),
					updated_at: iso(now())
				}
			],
			limit: 200,
			offset: 0,
			total: 1
		});
	if (p === '/v1/sites/s1/devices')
		return send(res, 200, {
			items: [
				{
					id: 'dv1',
					site_id: 's1',
					device_key: 'esp32-01',
					name: 'Sensor 1',
					expected_interval_s: 60,
					low_batt_v: 3.3,
					last_seen_at: iso(now()),
					last_status: 'ONLINE',
					created_at: iso(now())
				}
			],
			limit: 100,
			offset: 0,
			total: 1
		});
	if (p === '/v1/devices/dv1/health')
		return send(res, 200, {
			status: 'ONLINE',
			last_seen_at: iso(now()),
			stale: false,
			health_score: 0.94,
			readings_last_hour: 58,
			battery_slope_v_per_day: -0.05,
			last_reading: {
				ts: iso(now() - 30_000),
				raw_mm: 3000,
				level_cm: 150,
				batt_v: 3.9,
				quality_score: 1
			},
			evaluated_at: iso(now())
		});
	if (p === '/v1/devices/dv1/calibrations') {
		if (req.method === 'POST') {
			calls.push({ path: p, body: JSON.parse(raw) });
			return send(res, 201, { version: 2 });
		}
		return send(res, 200, {
			items: [
				{
					version: 1,
					offset_cm: 450,
					min_valid_mm: 250,
					max_valid_mm: 4500,
					valid_from: '2026-01-01T00:00:00Z',
					note: 'awal'
				}
			],
			limit: 200,
			offset: 0,
			total: 1
		});
	}
	return send(res, 404, { error: { code: 'not_found', message: 'not found' } });
});

server.listen(PORT, '127.0.0.1', () => console.log(`mock API and OIDC on ${BASE}`));
