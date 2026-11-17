import { describe, expect, test } from 'vitest';
import { deriveFreshness, isFreshReading, MIN_STALE_MS } from './stale';

const now = 1_000_000_000;

describe('deriveFreshness (fail-visible)', () => {
	test('fresh server answer is not stale', () => {
		expect(
			deriveFreshness({
				apiStale: false,
				lastOkMs: now - 10_000,
				nowMs: now,
				liveReadingAtMs: null
			})
		).toEqual({
			stale: false,
			connectionLost: false
		});
	});

	test('the server saying stale shows the label', () => {
		const f = deriveFreshness({
			apiStale: true,
			lastOkMs: now - 1000,
			nowMs: now,
			liveReadingAtMs: null
		});
		expect(f.stale).toBe(true);
		expect(f.connectionLost).toBe(false);
	});

	test('a live reading newer than the server answer clears a stale flag', () => {
		const f = deriveFreshness({
			apiStale: true,
			lastOkMs: now - 20_000,
			nowMs: now,
			liveReadingAtMs: now - 5_000
		});
		expect(f.stale).toBe(false);
	});

	test('a live reading older than 5 minutes no longer vouches for freshness', () => {
		const f = deriveFreshness({
			apiStale: true,
			lastOkMs: now - 1000,
			nowMs: now,
			liveReadingAtMs: now - MIN_STALE_MS - 1
		});
		expect(f.stale).toBe(true);
	});

	test('losing the server for more than 5 minutes turns the label on even if the last answer was fresh', () => {
		const f = deriveFreshness({
			apiStale: false,
			lastOkMs: now - MIN_STALE_MS - 1,
			nowMs: now,
			liveReadingAtMs: null
		});
		expect(f).toEqual({ stale: true, connectionLost: true });
	});

	test('exactly 5 minutes of silence is still tolerated', () => {
		expect(
			deriveFreshness({
				apiStale: false,
				lastOkMs: now - MIN_STALE_MS,
				nowMs: now,
				liveReadingAtMs: null
			}).stale
		).toBe(false);
	});

	test('live readings keep the page fresh while polling is down', () => {
		const f = deriveFreshness({
			apiStale: false,
			lastOkMs: now - 10 * 60_000,
			nowMs: now,
			liveReadingAtMs: now - 30_000
		});
		expect(f).toEqual({ stale: false, connectionLost: false });
	});
});

describe('isFreshReading (a live event must be recent by its own timestamp)', () => {
	test('a reading measured moments ago vouches for freshness', () => {
		expect(isFreshReading(now - 30_000, now)).toBe(true);
		expect(isFreshReading(now - MIN_STALE_MS, now)).toBe(true);
	});

	test('a reading with an old timestamp does not, even if it only just arrived', () => {
		expect(isFreshReading(now - MIN_STALE_MS - 1, now)).toBe(false);
		expect(isFreshReading(now - 3 * 3_600_000, now)).toBe(false);
	});

	test('a device clock slightly ahead is tolerated', () => {
		expect(isFreshReading(now + 10_000, now)).toBe(true);
	});
});
