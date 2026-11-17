import { describe, expect, test } from 'vitest';
import { chooseResolution, isRangeId, rangeWindow, RANGE_IDS } from './resolution';

const H = 3_600_000;

describe('chooseResolution mirrors the API (store.ChooseResolution)', () => {
	test.each([
		[1 * H, 'raw'],
		[6 * H, 'raw'],
		[6 * H + 1, '1m'],
		[24 * H, '1m'],
		[48 * H, '1m'],
		[48 * H + 1, '1h'],
		[7 * 24 * H, '1h'],
		[30 * 24 * H, '1h'],
		[30 * 24 * H + 1, '1d'],
		[400 * 24 * H, '1d']
	])('span %d ms gives %s', (span, want) => {
		expect(chooseResolution(span)).toBe(want);
	});

	test('the four UI ranges map to raw, 1m, 1h, 1h', () => {
		const got = RANGE_IDS.map((id) => chooseResolution(rangeWindow(id, 0).spanMs));
		expect(got).toEqual(['raw', '1m', '1h', '1h']);
	});
});

describe('rangeWindow', () => {
	test('ends now and starts span earlier, as RFC 3339', () => {
		const now = Date.UTC(2026, 10, 16, 12, 0, 0);
		const w = rangeWindow('24h', now);
		expect(w.to).toBe('2026-11-16T12:00:00.000Z');
		expect(w.from).toBe('2026-11-15T12:00:00.000Z');
	});
	test('isRangeId only accepts the four ranges', () => {
		expect(isRangeId('7d')).toBe(true);
		expect(isRangeId('1y')).toBe(false);
		expect(isRangeId(null)).toBe(false);
	});
});
