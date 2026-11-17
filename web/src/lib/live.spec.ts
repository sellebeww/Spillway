import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { isLiveReading, openLive } from './live';

class FakeES {
	static CLOSED = 2;
	static instances: FakeES[] = [];
	readyState = 0;
	closed = false;
	onopen: (() => void) | null = null;
	onerror: (() => void) | null = null;
	listeners = new Map<string, (e: unknown) => void>();
	constructor(public url: string) {
		FakeES.instances.push(this);
	}
	addEventListener(n: string, f: (e: unknown) => void) {
		this.listeners.set(n, f);
	}
	close() {
		this.closed = true;
		this.readyState = 2;
	}
	emit(data: string) {
		this.listeners.get('reading')?.({ data });
	}
}

beforeEach(() => {
	FakeES.instances = [];
	vi.stubGlobal('EventSource', FakeES);
	vi.useFakeTimers();
});
afterEach(() => {
	vi.useRealTimers();
	vi.unstubAllGlobals();
});

const reading = { site: 's', device_id: 'd', ts: 1, level_cm: 12.5, late: false };

describe('openLive', () => {
	test('delivers valid readings and drops malformed ones', () => {
		const got: unknown[] = [];
		openLive('http://api', 's', { onReading: (r) => got.push(r), onState: () => {} });
		const es = FakeES.instances[0];
		expect(es.url).toBe('http://api/v1/public/stream?site=s');
		es.emit(JSON.stringify(reading));
		es.emit('not json');
		es.emit(JSON.stringify({ site: 's', level_cm: 'x' }));
		expect(got).toEqual([reading]);
	});

	test('close() closes the stream and cancels a pending retry (nothing leaks)', () => {
		const h = openLive('http://api', null, { onReading: () => {}, onState: () => {} });
		const es = FakeES.instances[0];
		expect(es.url).toBe('http://api/v1/public/stream');
		es.readyState = 2;
		es.onerror?.(); // the server refused: a retry is scheduled
		h.close();
		vi.advanceTimersByTime(120_000);
		expect(FakeES.instances).toHaveLength(1);
		expect(es.closed).toBe(true);
	});

	test('retries with a growing delay after the server closes the stream', () => {
		openLive('http://api', 's', { onReading: () => {}, onState: () => {} });
		FakeES.instances[0].readyState = 2;
		FakeES.instances[0].onerror?.();
		vi.advanceTimersByTime(3999);
		expect(FakeES.instances).toHaveLength(1);
		vi.advanceTimersByTime(2);
		expect(FakeES.instances).toHaveLength(2);
	});

	test('a transient error leaves reconnecting to the browser (no second connection)', () => {
		const states: string[] = [];
		openLive('http://api', 's', { onReading: () => {}, onState: (s) => states.push(s) });
		FakeES.instances[0].readyState = 0;
		FakeES.instances[0].onerror?.();
		vi.advanceTimersByTime(60_000);
		expect(FakeES.instances).toHaveLength(1);
		expect(states).toEqual(['connecting', 'reconnecting']);
	});
});

describe('isLiveReading', () => {
	test('rejects wrong shapes', () => {
		expect(isLiveReading(reading)).toBe(true);
		expect(isLiveReading(null)).toBe(false);
		expect(isLiveReading({ ...reading, ts: Number.NaN })).toBe(false);
		expect(isLiveReading({ ...reading, site: 1 })).toBe(false);
	});
});
