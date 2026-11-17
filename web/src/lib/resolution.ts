// Copyright (c) 2026 Vassel Goleyu. MIT License.

export const RANGE_IDS = ['1h', '24h', '7d', '30d'] as const;
export type RangeId = (typeof RANGE_IDS)[number];
export type Resolution = 'raw' | '1m' | '1h' | '1d';

const HOUR = 3_600_000;
const SPAN_MS: Record<RangeId, number> = {
	'1h': HOUR,
	'24h': 24 * HOUR,
	'7d': 7 * 24 * HOUR,
	'30d': 30 * 24 * HOUR
};

export function isRangeId(v: string | null | undefined): v is RangeId {
	return (RANGE_IDS as readonly string[]).includes(v ?? '');
}

/** The time window for a range ending now, as RFC 3339 strings the API accepts. */
export function rangeWindow(
	id: RangeId,
	nowMs: number
): { from: string; to: string; spanMs: number } {
	const spanMs = SPAN_MS[id];
	return {
		from: new Date(nowMs - spanMs).toISOString(),
		to: new Date(nowMs).toISOString(),
		spanMs
	};
}

/**
 * Mirrors the API's automatic choice (store.ChooseResolution, docs/SPEC.md 5.6): raw up to 6 h,
 * 1 minute up to 48 h, 1 hour up to 30 days, 1 day beyond. The API decides; this only labels
 * and sanity-checks what comes back.
 */
export function chooseResolution(spanMs: number): Resolution {
	if (spanMs <= 6 * HOUR) return 'raw';
	if (spanMs <= 48 * HOUR) return '1m';
	if (spanMs <= 30 * 24 * HOUR) return '1h';
	return '1d';
}

export function isResolution(v: unknown): v is Resolution {
	return v === 'raw' || v === '1m' || v === '1h' || v === '1d';
}
