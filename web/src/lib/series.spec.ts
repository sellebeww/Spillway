import { describe, expect, test } from 'vitest';
import { appendPoint, riseRateCmPerHour, toChartPoints, trendOf, type ChartPoint } from './series';

const now = Date.UTC(2026, 10, 16, 12, 0, 0);
const min = 60_000;

function line(slopeCmPerHour: number, count: number, spacingMs: number, endMs = now): ChartPoint[] {
	return Array.from({ length: count }, (_, i) => {
		const t = endMs - (count - 1 - i) * spacingMs;
		return { t, v: 100 + (slopeCmPerHour * (t - (endMs - (count - 1) * spacingMs))) / 3_600_000 };
	});
}

describe('riseRateCmPerHour', () => {
	test('recovers the slope of a clean line (OLS)', () => {
		expect(riseRateCmPerHour(line(30, 11, min), now)).toBeCloseTo(30, 6);
		expect(riseRateCmPerHour(line(-12, 11, min), now)).toBeCloseTo(-12, 6);
		expect(riseRateCmPerHour(line(0, 11, min), now)).toBeCloseTo(0, 9);
	});

	test('needs at least 4 points', () => {
		expect(riseRateCmPerHour(line(30, 3, 3 * min), now)).toBeNull();
		expect(riseRateCmPerHour(line(30, 4, 2 * min), now)).not.toBeNull();
	});

	test('needs a span of at least 5 minutes', () => {
		expect(riseRateCmPerHour(line(30, 5, 60_000), now)).toBeNull(); // 4 min span
		expect(riseRateCmPerHour(line(30, 6, 60_000), now)).not.toBeNull(); // exactly 5 min
	});

	test('ignores points older than 10 minutes', () => {
		const old = line(500, 20, min, now - 30 * min);
		const fresh = line(6, 8, min);
		expect(riseRateCmPerHour([...old, ...fresh], now)).toBeCloseTo(6, 6);
	});

	test('identical timestamps never produce NaN', () => {
		const same = [0, 0, 0, 0, 0].map((_, i) => ({ t: now - 6 * min, v: i }));
		same.push({ t: now, v: 1 });
		const r = riseRateCmPerHour(same, now);
		expect(r === null || Number.isFinite(r)).toBe(true);
	});
});

describe('trendOf', () => {
	test('uses a steady band so noise is not called a trend', () => {
		expect(trendOf(0.2)).toBe('steady');
		expect(trendOf(-0.5)).toBe('steady');
		expect(trendOf(3)).toBe('rising');
		expect(trendOf(-3)).toBe('falling');
	});
});

describe('toChartPoints', () => {
	test('reads raw points and aggregates, sorted, dropping malformed ones', () => {
		const raw = toChartPoints({
			points: [
				{ ts: '2026-11-16T10:02:00Z', level_cm: 12 },
				{ ts: '2026-11-16T10:01:00Z', level_cm: 11 },
				{ ts: 'nope', level_cm: 5 },
				{ ts: '2026-11-16T10:03:00Z', level_cm: Number.NaN }
			]
		});
		expect(raw.map((p) => p.v)).toEqual([11, 12]);
		const agg = toChartPoints({
			points: [{ ts: '2026-11-16T10:00:00Z', min: 1, avg: 2, max: 3, n: 4 }]
		});
		expect(agg).toEqual([{ t: Date.parse('2026-11-16T10:00:00Z'), v: 2, min: 1, max: 3 }]);
	});
});

describe('appendPoint', () => {
	test('keeps order, ignores duplicates and caps the size', () => {
		let pts: ChartPoint[] = [];
		for (const t of [3, 1, 2, 2]) pts = appendPoint(pts, { t, v: t }, 3);
		expect(pts.map((p) => p.t)).toEqual([1, 2, 3]);
		pts = appendPoint(pts, { t: 4, v: 4 }, 3);
		expect(pts.map((p) => p.t)).toEqual([2, 3, 4]);
	});
});
