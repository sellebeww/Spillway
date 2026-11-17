import { describe, expect, test } from 'vitest';
import {
	formatAge,
	formatDateTime,
	formatLevel,
	isValidTimeZone,
	utcToZonedLocal,
	zoneAbbr,
	zoneOffsetMinutes,
	zonedLocalToUtc
} from './format';

describe('site time zone', () => {
	const t = '2026-11-16T11:05:00Z';
	test('Asia/Jakarta is WIB, +7', () => {
		expect(formatDateTime(t, 'Asia/Jakarta')).toBe('16 Nov 2026 18.05 WIB');
	});
	test('Makassar is WITA, Jayapura is WIT', () => {
		expect(formatDateTime(t, 'Asia/Makassar')).toBe('16 Nov 2026 19.05 WITA');
		expect(formatDateTime(t, 'Asia/Jayapura')).toBe('16 Nov 2026 20.05 WIT');
	});
	test('crosses midnight into the next local day', () => {
		expect(formatDateTime('2026-12-31T20:30:00Z', 'Asia/Jakarta')).toBe('1 Jan 2027 03.30 WIB');
	});
	test('uses Indonesian month names', () => {
		expect(formatDateTime('2026-05-01T00:00:00Z', 'Asia/Jakarta')).toContain('Mei');
		expect(formatDateTime('2026-08-01T00:00:00Z', 'Asia/Jakarta')).toContain('Agu');
	});
	test('an unknown zone falls back to Jakarta, bad input to a dash', () => {
		expect(formatDateTime(t, 'Mars/Olympus')).toBe('16 Nov 2026 18.05 WIB');
		expect(formatDateTime('not a date', 'Asia/Jakarta')).toBe('-');
		expect(formatDateTime(null, 'Asia/Jakarta')).toBe('-');
	});
	test('other zones show the platform short name', () => {
		expect(zoneAbbr('UTC', new Date(t))).toBe('UTC');
	});
});

describe('zone conversion for the calibration start time', () => {
	test('offset of WIB is +420 minutes', () => {
		expect(zoneOffsetMinutes(new Date('2026-11-16T00:00:00Z'), 'Asia/Jakarta')).toBe(420);
	});
	test('wall-clock time in Jakarta to UTC', () => {
		expect(zonedLocalToUtc('2026-11-16T18:05', 'Asia/Jakarta')?.toISOString()).toBe(
			'2026-11-16T11:05:00.000Z'
		);
	});
	test('round trip', () => {
		const d = new Date('2026-11-16T11:05:00Z');
		expect(utcToZonedLocal(d, 'Asia/Jakarta')).toBe('2026-11-16T18:05');
		expect(
			zonedLocalToUtc(utcToZonedLocal(d, 'Asia/Makassar'), 'Asia/Makassar')?.toISOString()
		).toBe(d.toISOString());
	});
	test('handles a zone with daylight saving time', () => {
		// 2026-07-01 12:00 in Europe/Berlin is CEST (UTC+2).
		expect(zonedLocalToUtc('2026-07-01T12:00', 'Europe/Berlin')?.toISOString()).toBe(
			'2026-07-01T10:00:00.000Z'
		);
		expect(zonedLocalToUtc('2026-12-01T12:00', 'Europe/Berlin')?.toISOString()).toBe(
			'2026-12-01T11:00:00.000Z'
		);
	});
	test('rejects impossible dates and bad zones', () => {
		expect(zonedLocalToUtc('2026-02-30T10:00', 'Asia/Jakarta')).toBeNull();
		expect(zonedLocalToUtc('2026-11-16T25:00', 'Asia/Jakarta')).toBeNull();
		expect(zonedLocalToUtc('garbage', 'Asia/Jakarta')).toBeNull();
		expect(zonedLocalToUtc('2026-11-16T10:00', 'Nope/Zone')).toBeNull();
	});
	test('isValidTimeZone', () => {
		expect(isValidTimeZone('Asia/Jakarta')).toBe(true);
		expect(isValidTimeZone('Mars/Olympus')).toBe(false);
		expect(isValidTimeZone('')).toBe(false);
	});
});

describe('numbers and ages', () => {
	test('level uses a decimal comma and one digit', () => {
		expect(formatLevel(123.45)).toBe('123,5 cm');
		expect(formatLevel(-5)).toBe('-5 cm');
		expect(formatLevel(null)).toBe('-');
		expect(formatLevel(Number.NaN)).toBe('-');
	});
	test('relative age', () => {
		const now = Date.parse('2026-11-16T12:00:00Z');
		expect(formatAge('2026-11-16T11:59:40Z', now)).toBe('baru saja');
		expect(formatAge('2026-11-16T11:50:00Z', now)).toBe('10 menit lalu');
		expect(formatAge('2026-11-16T09:00:00Z', now)).toBe('3 jam lalu');
		expect(formatAge('2026-11-13T12:00:00Z', now)).toBe('3 hari lalu');
	});
});
