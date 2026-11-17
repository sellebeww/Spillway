import { describe, expect, test } from 'vitest';
import { originOf, safeTelegramUrl, withExtraOrigins } from './security';

describe('safeTelegramUrl', () => {
	test('accepts only t.me deep links', () => {
		expect(safeTelegramUrl('https://t.me/spillway_bot?start=abc_DEF-1')).toBe(
			'https://t.me/spillway_bot?start=abc_DEF-1'
		);
		expect(safeTelegramUrl('https://t.me/spillway_bot')).toBeDefined();
		for (const bad of [
			'http://t.me/x_bot',
			'https://evil.example/t.me/x_bot',
			'javascript:alert(1)',
			'https://t.me/x_bot?start=<script>',
			42,
			undefined
		]) {
			expect(safeTelegramUrl(bad)).toBeUndefined();
		}
	});
});

describe('originOf', () => {
	test('plain tile and API URLs', () => {
		expect(originOf('https://tile.openstreetmap.org/{z}/{x}/{y}.png')).toBe(
			'https://tile.openstreetmap.org'
		);
		expect(originOf('http://127.0.0.1:8080')).toBe('http://127.0.0.1:8080');
	});
	test('a placeholder host becomes a wildcard label', () => {
		expect(originOf('https://{s}.tile.example.com/{z}/{x}/{y}.png')).toBe(
			'https://*.tile.example.com'
		);
	});
	test('garbage or empty gives null', () => {
		expect(originOf('')).toBeNull();
		expect(originOf(undefined)).toBeNull();
		expect(originOf('ftp://x')).toBeNull();
		expect(originOf('not a url')).toBeNull();
	});
});

describe('withExtraOrigins', () => {
	const base = "default-src 'self'; connect-src 'self'; img-src 'self' data:";
	test('appends to existing directives and creates missing ones', () => {
		const out = withExtraOrigins(base, {
			'connect-src': ['https://api.x'],
			'worker-src': ['blob:']
		});
		expect(out).toContain("connect-src 'self' https://api.x");
		expect(out).toContain('worker-src blob:');
		expect(out).toContain("default-src 'self'");
	});
	test('does not duplicate and skips nulls', () => {
		const out = withExtraOrigins(base, { 'connect-src': ["'self'", null], 'img-src': [null] });
		expect(out).toBe(base);
	});
});
