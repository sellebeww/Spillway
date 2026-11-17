// Copyright (c) 2026 Vassel Goleyu. MIT License.
// Fail-visible freshness: stale data must never look current (docs/SPEC.md section 1).

/** The smallest staleness window the server uses (max(2 x interval, 5 minutes)). */
export const MIN_STALE_MS = 5 * 60_000;

/**
 * A live event may only vouch for freshness when the reading itself is recent. A buffered or
 * clock-skewed reading arrives "now" but was measured long ago, and must not clear DATA TERTUNDA.
 */
export function isFreshReading(tsMs: number, nowMs: number): boolean {
	return nowMs - tsMs <= MIN_STALE_MS;
}

export interface FreshnessInput {
	/** The server's own judgement from the last successful fetch. */
	apiStale: boolean;
	/** Client time of the last successful fetch from the server. */
	lastOkMs: number;
	nowMs: number;
	/** Client time a live reading arrived since that fetch, or null. */
	liveReadingAtMs: number | null;
}

export interface Freshness {
	stale: boolean;
	/** True when we cannot even reach the server, so nothing on screen can be trusted as current. */
	connectionLost: boolean;
}

/**
 * Decides whether to show DATA TERTUNDA. A recent live reading overrides a stale server flag that
 * is older than the reading; losing contact with the server for 5 minutes turns the label on by itself.
 */
export function deriveFreshness(i: FreshnessInput): Freshness {
	const liveFresh = i.liveReadingAtMs !== null && i.nowMs - i.liveReadingAtMs <= MIN_STALE_MS;
	const serverSilent = i.nowMs - i.lastOkMs > MIN_STALE_MS;
	const connectionLost = serverSilent && !liveFresh;
	return { connectionLost, stale: connectionLost || (i.apiStale && !liveFresh) };
}
