import { describe, expect, test } from 'vitest';
import { PendingStore, SessionStore } from './session';

const data = { accessToken: 'a', expiresAt: 0 };

describe('SessionStore', () => {
	test('ids are unguessable and unique', () => {
		const s = new SessionStore();
		const ids = new Set(Array.from({ length: 50 }, () => s.create(data).id));
		expect(ids.size).toBe(50);
		expect([...ids][0].length).toBeGreaterThanOrEqual(43); // 256 bits, base64url
	});

	test('expires after the absolute lifetime', () => {
		let t = 0;
		const s = new SessionStore(1000, 10, () => t);
		const { id } = s.create(data);
		t = 999;
		expect(s.get(id)).toBeDefined();
		t = 1001;
		expect(s.get(id)).toBeUndefined();
		expect(s.size).toBe(0);
	});

	test('delete signs out, unknown and empty ids give nothing', () => {
		const s = new SessionStore();
		const { id } = s.create(data);
		s.delete(id);
		expect(s.get(id)).toBeUndefined();
		expect(s.get(undefined)).toBeUndefined();
		expect(s.get('nope')).toBeUndefined();
	});

	test('the store is bounded: the oldest session is evicted first', () => {
		const s = new SessionStore(1_000_000, 3);
		const ids = [1, 2, 3, 4].map(() => s.create(data).id);
		expect(s.size).toBe(3);
		expect(s.get(ids[0])).toBeUndefined();
		expect(s.get(ids[3])).toBeDefined();
	});
});

describe('PendingStore', () => {
	const p = { verifier: 'v', nonce: 'n', next: '/admin/sites' };

	test('a state works exactly once', () => {
		const s = new PendingStore();
		s.put('st', p);
		expect(s.take('st')).toEqual(p);
		expect(s.take('st')).toBeUndefined();
	});

	test('a state expires', () => {
		let t = 0;
		const s = new PendingStore(1000, 10, () => t);
		s.put('st', p);
		t = 1001;
		expect(s.take('st')).toBeUndefined();
	});

	test('unknown or missing state gives nothing, and the store is bounded', () => {
		const s = new PendingStore(1000, 2);
		expect(s.take('x')).toBeUndefined();
		expect(s.take(null)).toBeUndefined();
		s.put('a', p);
		s.put('b', p);
		s.put('c', p);
		expect(s.take('a')).toBeUndefined();
		expect(s.take('c')).toEqual(p);
	});
});
