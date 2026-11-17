// Copyright (c) 2026 Vassel Goleyu. MIT License.
// Live readings over Server-Sent Events, with an explicit close so nothing leaks when a page goes away.
import type { LiveReading } from './types';

export type LiveState = 'connecting' | 'open' | 'reconnecting';

export interface LiveHandle {
	close(): void;
}

export function isLiveReading(x: unknown): x is LiveReading {
	if (!x || typeof x !== 'object') return false;
	const r = x as Record<string, unknown>;
	return (
		typeof r.site === 'string' &&
		typeof r.device_id === 'string' &&
		typeof r.ts === 'number' &&
		Number.isFinite(r.ts) &&
		typeof r.level_cm === 'number' &&
		Number.isFinite(r.level_cm)
	);
}

/**
 * Opens the public stream (one site, or all when slug is null). The browser reconnects by itself after
 * a dropped connection; when the server refuses (the stream becomes CLOSED, for example after a 429 or 503)
 * we retry with a growing delay. close() stops everything, including a pending retry.
 */
export function openLive(
	base: string,
	slug: string | null,
	h: { onReading(r: LiveReading): void; onState(s: LiveState): void }
): LiveHandle {
	const url = `${base.replace(/\/$/, '')}/v1/public/stream${slug ? `?site=${encodeURIComponent(slug)}` : ''}`;
	let es: EventSource | null = null;
	let timer: ReturnType<typeof setTimeout> | undefined;
	let closed = false;
	let failures = 0;

	const connect = () => {
		if (closed) return;
		h.onState('connecting');
		es = new EventSource(url);
		es.addEventListener('reading', (e) => {
			try {
				const r: unknown = JSON.parse((e as MessageEvent<string>).data);
				if (isLiveReading(r)) h.onReading(r);
			} catch {
				/* a malformed event is skipped, never shown */
			}
		});
		es.onopen = () => {
			failures = 0;
			h.onState('open');
		};
		es.onerror = () => {
			if (closed || !es) return;
			h.onState('reconnecting');
			if (es.readyState === EventSource.CLOSED) {
				es.close();
				es = null;
				failures++;
				timer = setTimeout(connect, Math.min(30_000, 2000 * 2 ** Math.min(failures, 4)));
			}
		};
	};
	connect();

	return {
		close() {
			closed = true;
			clearTimeout(timer);
			es?.close();
			es = null;
		}
	};
}
