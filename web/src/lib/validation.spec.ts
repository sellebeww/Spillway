import { describe, expect, test } from 'vitest';
import {
	isBackdated,
	parseNumber,
	previewLevel,
	slugify,
	validateCalibration,
	validateDevice,
	validateEmail,
	validateRule,
	validateSite,
	type CalibrationInput,
	type RuleInput
} from './validation';

const rule = (over: Partial<RuleInput> = {}): RuleInput => ({
	type: 'THRESHOLD',
	tiers: [
		{ value: '150', severity: '1' },
		{ value: '200', severity: '2' }
	],
	hysteresis_cm: '',
	cooldown_min: '',
	min_quality: '',
	enabled: true,
	...over
});

describe('parseNumber', () => {
	test('accepts decimal comma and dot', () => {
		expect(parseNumber('12,5')).toBe(12.5);
		expect(parseNumber(' -3.25 ')).toBe(-3.25);
	});
	test.each(['', 'abc', '1e5', 'Infinity', '0x10', '1,2,3', '--1', '12 5'])('rejects %j', (s) => {
		expect(parseNumber(s)).toBeNull();
	});
});

describe('validateRule', () => {
	test('a valid THRESHOLD rule gets the server defaults', () => {
		const r = validateRule(rule());
		expect(r).toEqual({
			ok: true,
			value: {
				type: 'THRESHOLD',
				tiers: [
					{ value: 150, severity: 1 },
					{ value: 200, severity: 2 }
				],
				cooldown_s: 1800,
				min_quality: 0.5,
				enabled: true
			}
		});
	});

	test('hysteresis is sent for THRESHOLD only and cooldown is converted to seconds', () => {
		const r = validateRule(rule({ hysteresis_cm: '7,5', cooldown_min: '10', min_quality: '0,8' }));
		expect(r.ok && r.value).toMatchObject({
			hysteresis_cm: 7.5,
			cooldown_s: 600,
			min_quality: 0.8
		});
		const rr = validateRule(rule({ type: 'RATE_OF_RISE', hysteresis_cm: '9' }));
		expect(rr.ok && 'hysteresis_cm' in rr.value).toBe(false);
	});

	test('tiers must ascend in value and in severity', () => {
		const flat = validateRule(
			rule({
				tiers: [
					{ value: '150', severity: '1' },
					{ value: '150', severity: '2' }
				]
			})
		);
		expect(!flat.ok && flat.errors['tiers.1.value']).toBeTruthy();
		const sev = validateRule(
			rule({
				tiers: [
					{ value: '150', severity: '2' },
					{ value: '200', severity: '2' }
				]
			})
		);
		expect(!sev.ok && sev.errors['tiers.1.severity']).toBeTruthy();
	});

	test('rejects bad numbers, no tiers, four tiers and severity outside 1..3', () => {
		const bad = validateRule(rule({ tiers: [{ value: 'abc', severity: '4' }] }));
		expect(!bad.ok && Object.keys(bad.errors).sort()).toEqual([
			'tiers.0.severity',
			'tiers.0.value'
		]);
		expect(validateRule(rule({ tiers: [] })).ok).toBe(false);
		const four = Array.from({ length: 4 }, (_, i) => ({ value: String(100 + i), severity: '1' }));
		expect(validateRule(rule({ tiers: four })).ok).toBe(false);
		const neg = validateRule(rule({ tiers: [{ value: '-1', severity: '1' }] }));
		expect(neg.ok).toBe(false);
		expect(validateRule(rule({ tiers: [{ value: '10001', severity: '1' }] })).ok).toBe(false);
	});

	test('limits of the optional fields', () => {
		expect(validateRule(rule({ hysteresis_cm: '0' })).ok).toBe(false);
		expect(validateRule(rule({ hysteresis_cm: '1001' })).ok).toBe(false);
		expect(validateRule(rule({ cooldown_min: '0' })).ok).toBe(false);
		expect(validateRule(rule({ cooldown_min: '1441' })).ok).toBe(false);
		expect(validateRule(rule({ cooldown_min: '1,5' })).ok).toBe(false);
		expect(validateRule(rule({ min_quality: '0' })).ok).toBe(false);
		expect(validateRule(rule({ min_quality: '1,1' })).ok).toBe(false);
		expect(
			validateRule(rule({ min_quality: '1', cooldown_min: '1440', hysteresis_cm: '1000' })).ok
		).toBe(true);
	});

	test('unknown type is rejected', () => {
		expect(validateRule(rule({ type: 'OTHER' })).ok).toBe(false);
	});
});

describe('calibration', () => {
	const ctx = {
		tz: 'Asia/Jakarta',
		nowMs: Date.parse('2026-11-16T12:00:00Z'),
		latestFromMs: Date.parse('2026-11-01T00:00:00Z')
	};
	const cal = (over: Partial<CalibrationInput> = {}): CalibrationInput => ({
		offset_cm: '450',
		min_valid_mm: '250',
		max_valid_mm: '4500',
		valid_from: '2026-11-16T19:00',
		note: '',
		confirm_backdate: false,
		...over
	});

	test('valid input converts the start time from the site zone to UTC', () => {
		const r = validateCalibration(cal(), ctx);
		expect(r.ok && r.value).toMatchObject({
			offset_cm: 450,
			valid_from: '2026-11-16T12:00:00.000Z'
		});
	});

	test.each([
		['offset_cm', { offset_cm: 'abc' }],
		['offset_cm', { offset_cm: '' }],
		['offset_cm', { offset_cm: '100001' }],
		['min_valid_mm', { min_valid_mm: '4500', max_valid_mm: '250' }],
		['min_valid_mm', { min_valid_mm: '250', max_valid_mm: '250' }],
		['min_valid_mm', { min_valid_mm: '-1' }],
		['max_valid_mm', { max_valid_mm: '12.5' }],
		['valid_from', { valid_from: '' }],
		['valid_from', { valid_from: '2026-02-30T10:00' }],
		['valid_from', { valid_from: '2028-01-01T00:00' }],
		['note', { note: 'x'.repeat(501) }]
	])('rejects an invalid %s', (field, over) => {
		const r = validateCalibration(cal(over as Partial<CalibrationInput>), ctx);
		expect(r.ok).toBe(false);
		expect(!r.ok && r.errors[field]).toBeTruthy();
	});

	test('backdating needs explicit confirmation, like the API (409 backdated)', () => {
		const early = cal({ valid_from: '2026-10-20T08:00' });
		expect(isBackdated(early.valid_from, ctx.tz, ctx.latestFromMs)).toBe(true);
		const refused = validateCalibration(early, ctx);
		expect(!refused.ok && refused.errors.confirm_backdate).toBeTruthy();
		expect(validateCalibration({ ...early, confirm_backdate: true }, ctx).ok).toBe(true);
	});

	test('the first version is never backdated', () => {
		expect(isBackdated('2020-01-01T00:00', 'Asia/Jakarta', null)).toBe(false);
	});

	test('preview follows level_cm = offset_cm - raw_mm / 10', () => {
		expect(previewLevel(450, 3000)).toBe(150);
		expect(previewLevel(450.5, 1234)).toBeCloseTo(327.1, 9);
	});
});

describe('sites, devices, e-mail', () => {
	const site = {
		name: 'Jembatan Kali Putih',
		slug: 'kali-putih',
		lat: '-7,5',
		lon: '110.25',
		tz: 'Asia/Jakarta',
		is_public: true
	};

	test('a valid site', () => {
		const r = validateSite(site);
		expect(r.ok && r.value).toMatchObject({ lat: -7.5, lon: 110.25, slug: 'kali-putih' });
	});

	test.each([
		['name', { name: '  ' }],
		['slug', { slug: 'Bad Slug' }],
		['slug', { slug: 'a' }],
		['lat', { lat: '91' }],
		['lon', { lon: '-181' }],
		['lat', { lat: 'x' }],
		['tz', { tz: 'Mars/Olympus' }]
	])('rejects a bad %s', (field, over) => {
		const r = validateSite({ ...site, ...over });
		expect(!r.ok && r.errors[field]).toBeTruthy();
	});

	test('slugify', () => {
		expect(slugify('Jembatan Kali Putih')).toBe('jembatan-kali-putih');
		expect(slugify('  Sungai Cisadane (Hulu) #2 ')).toBe('sungai-cisadane-hulu-2');
		expect(slugify('Pasar Gede Surakarta é')).toBe('pasar-gede-surakarta-e');
	});

	test('device', () => {
		const ok = validateDevice({
			device_key: 'esp32-01',
			name: 'Sensor',
			expected_interval_s: '60',
			low_batt_v: '3,3'
		});
		expect(ok.ok && ok.value).toMatchObject({ expected_interval_s: 60, low_batt_v: 3.3 });
		expect(
			validateDevice({ device_key: 'a b', name: 'x', expected_interval_s: '60', low_batt_v: '3.3' })
				.ok
		).toBe(false);
		expect(
			validateDevice({ device_key: 'a.b', name: 'x', expected_interval_s: '60', low_batt_v: '3.3' })
				.ok
		).toBe(false);
		expect(
			validateDevice({ device_key: 'ok', name: 'x', expected_interval_s: '0', low_batt_v: '3.3' })
				.ok
		).toBe(false);
		expect(
			validateDevice({ device_key: 'ok', name: 'x', expected_interval_s: '60', low_batt_v: '21' })
				.ok
		).toBe(false);
	});

	test('e-mail', () => {
		expect(validateEmail(' a@b.co ').ok).toBe(true);
		expect(validateEmail('a@b').ok).toBe(false);
		expect(validateEmail('no at').ok).toBe(false);
	});
});
