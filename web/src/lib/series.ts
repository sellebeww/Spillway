// Copyright (c) 2026 Vassel Goleyu. MIT License.
import type { Resolution } from './resolution';

export interface RawPoint {
	ts: string;
	level_cm: number;
	late?: boolean;
}
export interface AggPoint {
	ts: string;
	min: number;
	avg: number;
	max: number;
	n: number;
}
export interface SeriesResponse {
	device_id: string;
	res: Resolution;
	truncated: boolean;
	points: Array<RawPoint | AggPoint>;
}

/** One chart point: time in ms, the line value, and the band for aggregates. */
export interface ChartPoint {
	t: number;
	v: number;
	min?: number;
	max?: number;
}

function isAgg(p: RawPoint | AggPoint): p is AggPoint {
	return 'avg' in p;
}

/** Normalises raw and aggregated points to one shape; drops malformed points and sorts by time. */
export function toChartPoints(resp: Pick<SeriesResponse, 'points'>): ChartPoint[] {
	const out: ChartPoint[] = [];
	for (const p of resp.points) {
		const t = Date.parse(p.ts);
		if (Number.isNaN(t)) continue;
		if (isAgg(p)) {
			if ([p.avg, p.min, p.max].every(Number.isFinite))
				out.push({ t, v: p.avg, min: p.min, max: p.max });
		} else if (Number.isFinite(p.level_cm)) {
			out.push({ t, v: p.level_cm });
		}
	}
	return out.sort((a, b) => a.t - b.t);
}

export const RISE_WINDOW_MS = 10 * 60_000;
const RISE_MIN_POINTS = 4;
const RISE_MIN_SPAN_MS = 5 * 60_000;

/**
 * Rate of rise in cm per hour: ordinary least squares over the readings of the last 10 minutes
 * (x in seconds), as in the RATE_OF_RISE rule. Needs 4 points spanning 5 minutes, else null.
 */
export function riseRateCmPerHour(points: ChartPoint[], nowMs: number): number | null {
	const win = points.filter((p) => p.t > nowMs - RISE_WINDOW_MS && p.t <= nowMs + 60_000);
	if (win.length < RISE_MIN_POINTS) return null;
	const first = Math.min(...win.map((p) => p.t));
	const last = Math.max(...win.map((p) => p.t));
	if (last - first < RISE_MIN_SPAN_MS) return null;
	const xs = win.map((p) => (p.t - first) / 1000);
	const n = win.length;
	const mx = xs.reduce((a, b) => a + b, 0) / n;
	const my = win.reduce((a, p) => a + p.v, 0) / n;
	let num = 0;
	let den = 0;
	for (let i = 0; i < n; i++) {
		num += (xs[i] - mx) * (win[i].v - my);
		den += (xs[i] - mx) ** 2;
	}
	if (den === 0) return null;
	return (num / den) * 3600;
}

export type Trend = 'rising' | 'falling' | 'steady';

/** Changes smaller than this per hour are called steady (sensor noise, not a trend). */
export function trendOf(ratePerHour: number, steadyBand = 0.5): Trend {
	if (ratePerHour > steadyBand) return 'rising';
	if (ratePerHour < -steadyBand) return 'falling';
	return 'steady';
}

/** Appends a live point, keeping time order, dropping old points and capping the size. */
export function appendPoint(points: ChartPoint[], p: ChartPoint, cap = 5000): ChartPoint[] {
	if (points.some((q) => q.t === p.t)) return points;
	const next = [...points, p].sort((a, b) => a.t - b.t);
	return next.length > cap ? next.slice(next.length - cap) : next;
}
