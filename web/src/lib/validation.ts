// Copyright (c) 2026 Vassel Goleyu. MIT License.
// Form validation shared by the browser (instant feedback) and the server actions (the real check).
// Limits mirror the API (internal/api/admin.go and the store) so the server never has to be the first to say no.

import { isValidTimeZone, zonedLocalToUtc } from './format';
import { messages } from './messages';

const v = messages.validation;

export type Errors = Record<string, string>;
export type Validated<T> = { ok: true; value: T } | { ok: false; errors: Errors };

const fail = (errors: Errors): { ok: false; errors: Errors } => ({ ok: false, errors });

const NUMBER_RE = /^-?\d+(\.\d+)?$/;

/** Parses a user-typed number. Accepts a decimal comma. Rejects "", "1e5", "Infinity", "0x10". */
export function parseNumber(raw: unknown): number | null {
	if (typeof raw === 'number') return Number.isFinite(raw) ? raw : null;
	if (typeof raw !== 'string') return null;
	const s = raw.trim().replace(',', '.');
	if (!NUMBER_RE.test(s)) return null;
	const n = Number(s);
	return Number.isFinite(n) ? n : null;
}

function num(
	raw: unknown,
	key: string,
	errors: Errors,
	min: number,
	max: number,
	integer = false
): number | null {
	if (raw === undefined || raw === null || String(raw).trim() === '') {
		errors[key] = v.required;
		return null;
	}
	const n = parseNumber(raw);
	if (n === null) {
		errors[key] = v.number;
		return null;
	}
	if (integer && !Number.isInteger(n)) {
		errors[key] = v.integer;
		return null;
	}
	if (n < min || n > max) {
		errors[key] = v.range(min, max);
		return null;
	}
	return n;
}

export const SLUG_RE = /^[a-z0-9][a-z0-9-]{1,62}$/;
export const DEVICE_KEY_RE = /^[A-Za-z0-9_-]{1,64}$/;

/** Suggests a slug from a site name ("Jembatan Kali Putih" gives "jembatan-kali-putih"). */
export function slugify(name: string): string {
	return name
		.normalize('NFKD')
		.replace(/[̀-ͯ]/g, '')
		.toLowerCase()
		.replace(/[^a-z0-9]+/g, '-')
		.replace(/^-+|-+$/g, '')
		.slice(0, 63);
}

// ---- sites ----

export interface SiteInput {
	name: string;
	slug: string;
	lat: string;
	lon: string;
	tz: string;
	is_public: boolean;
}
export interface SiteValue {
	name: string;
	slug: string;
	lat: number;
	lon: number;
	tz: string;
	is_public: boolean;
}

export function validateSite(raw: SiteInput): Validated<SiteValue> {
	const errors: Errors = {};
	const name = raw.name.trim();
	if (!name) errors.name = v.required;
	else if (name.length > 120) errors.name = v.nameLength;
	const slug = raw.slug.trim();
	if (!SLUG_RE.test(slug)) errors.slug = v.slug;
	const lat = num(raw.lat, 'lat', errors, -90, 90);
	const lon = num(raw.lon, 'lon', errors, -180, 180);
	const tz = raw.tz.trim();
	if (!isValidTimeZone(tz)) errors.tz = v.timezone;
	if (Object.keys(errors).length || lat === null || lon === null) return fail(errors);
	return { ok: true, value: { name, slug, lat, lon, tz, is_public: raw.is_public } };
}

export function validateEmail(raw: string): Validated<string> {
	const email = raw.trim();
	if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email) || email.length > 254)
		return fail({ email: v.email });
	return { ok: true, value: email };
}

// ---- devices ----

export interface DeviceInput {
	device_key: string;
	name: string;
	expected_interval_s: string;
	low_batt_v: string;
}
export interface DeviceValue {
	device_key: string;
	name: string;
	expected_interval_s: number;
	low_batt_v: number;
}

export function validateDevice(raw: DeviceInput): Validated<DeviceValue> {
	const errors: Errors = {};
	const key = raw.device_key.trim();
	if (!DEVICE_KEY_RE.test(key)) errors.device_key = v.deviceKey;
	const name = raw.name.trim();
	if (!name) errors.name = v.required;
	else if (name.length > 120) errors.name = v.nameLength;
	const interval = num(raw.expected_interval_s, 'expected_interval_s', errors, 1, 86400, true);
	const batt = num(raw.low_batt_v, 'low_batt_v', errors, 0.1, 20);
	if (Object.keys(errors).length || interval === null || batt === null) return fail(errors);
	return {
		ok: true,
		value: { device_key: key, name, expected_interval_s: interval, low_batt_v: batt }
	};
}

// ---- calibration ----

/** level_cm = offset_cm - raw_mm / 10 (docs/SPEC.md 5.2). */
export function previewLevel(offsetCm: number, rawMm: number): number {
	return offsetCm - rawMm / 10;
}

export interface CalibrationInput {
	offset_cm: string;
	min_valid_mm: string;
	max_valid_mm: string;
	valid_from: string; // "YYYY-MM-DDTHH:mm" wall-clock time in the site zone
	note: string;
	confirm_backdate: boolean;
}
export interface CalibrationContext {
	tz: string;
	nowMs: number;
	/** valid_from of the latest existing version, in ms, or null when there is none. */
	latestFromMs: number | null;
}
export interface CalibrationValue {
	offset_cm: number;
	min_valid_mm: number;
	max_valid_mm: number;
	valid_from: string; // ISO UTC
	note: string;
	confirm_backdate: boolean;
}

/** True when the new version starts before the latest existing one (needs explicit confirmation). */
export function isBackdated(
	validFromLocal: string,
	tz: string,
	latestFromMs: number | null
): boolean {
	if (latestFromMs === null) return false;
	const t = zonedLocalToUtc(validFromLocal, tz);
	return t !== null && t.getTime() < latestFromMs;
}

export function validateCalibration(
	raw: CalibrationInput,
	ctx: CalibrationContext
): Validated<CalibrationValue> {
	const errors: Errors = {};
	const offset = num(raw.offset_cm, 'offset_cm', errors, -100000, 100000);
	if (errors.offset_cm === v.range(-100000, 100000)) errors.offset_cm = v.offsetRange;
	const min = num(raw.min_valid_mm, 'min_valid_mm', errors, 0, 100000, true);
	const max = num(raw.max_valid_mm, 'max_valid_mm', errors, 1, 100000, true);
	if (min !== null && max !== null && min >= max) errors.min_valid_mm = v.minBelowMax;
	const from = zonedLocalToUtc(raw.valid_from, ctx.tz);
	if (!from) errors.valid_from = v.dateTime;
	else if (from.getTime() > ctx.nowMs + 366 * 86_400_000) errors.valid_from = v.dateFarFuture;
	const note = raw.note.trim();
	if (note.length > 500) errors.note = v.noteLength;
	if (
		from &&
		!errors.valid_from &&
		isBackdated(raw.valid_from, ctx.tz, ctx.latestFromMs) &&
		!raw.confirm_backdate
	) {
		errors.confirm_backdate = messages.calibration.backdateNeeded;
	}
	if (Object.keys(errors).length || offset === null || min === null || max === null || !from)
		return fail(errors);
	return {
		ok: true,
		value: {
			offset_cm: offset,
			min_valid_mm: min,
			max_valid_mm: max,
			valid_from: from.toISOString(),
			note,
			confirm_backdate: raw.confirm_backdate
		}
	};
}

// ---- rules ----

export type RuleType = 'THRESHOLD' | 'RATE_OF_RISE';

export interface RuleInput {
	type: string;
	tiers: Array<{ value: string; severity: string }>;
	hysteresis_cm: string;
	cooldown_min: string;
	min_quality: string;
	enabled: boolean;
}
export interface RuleValue {
	type: RuleType;
	tiers: Array<{ value: number; severity: number }>;
	hysteresis_cm?: number;
	cooldown_s: number;
	min_quality: number;
	enabled: boolean;
}

/**
 * Validates a rule the way the API will: 1 to 3 tiers whose values and severities strictly ascend,
 * hysteresis for THRESHOLD only, cooldown 1 to 1440 minutes, minimum quality 0.01 to 1.
 * Empty hysteresis, cooldown and quality fall back to the server defaults (5 cm, 30 min, 0.5).
 */
export function validateRule(raw: RuleInput): Validated<RuleValue> {
	const errors: Errors = {};
	if (raw.type !== 'THRESHOLD' && raw.type !== 'RATE_OF_RISE') errors.type = v.required;
	const type = raw.type as RuleType;
	const tiers: Array<{ value: number; severity: number }> = [];
	if (raw.tiers.length < 1 || raw.tiers.length > 3) errors.tiers = v.tierCount;
	raw.tiers.forEach((t, i) => {
		const value = num(t.value, `tiers.${i}.value`, errors, 0, 10000);
		const sev = parseNumber(t.severity);
		if (sev === null || ![1, 2, 3].includes(sev)) errors[`tiers.${i}.severity`] = v.severityInvalid;
		if (value !== null && sev !== null && [1, 2, 3].includes(sev))
			tiers.push({ value, severity: sev });
	});
	if (tiers.length === raw.tiers.length) {
		for (let i = 1; i < tiers.length; i++) {
			if (tiers[i].value <= tiers[i - 1].value) errors[`tiers.${i}.value`] = v.tiersAscend;
			if (tiers[i].severity <= tiers[i - 1].severity)
				errors[`tiers.${i}.severity`] = v.severitiesAscend;
		}
	}
	let hysteresis: number | undefined;
	if (type === 'THRESHOLD' && raw.hysteresis_cm.trim() !== '') {
		const h = num(raw.hysteresis_cm, 'hysteresis_cm', errors, 0.1, 1000);
		if (h !== null) hysteresis = h;
	}
	let cooldownMin = 30;
	if (raw.cooldown_min.trim() !== '') {
		const c = num(raw.cooldown_min, 'cooldown_min', errors, 1, 1440, true);
		if (c !== null) cooldownMin = c;
	}
	let minQuality = 0.5;
	if (raw.min_quality.trim() !== '') {
		const q = num(raw.min_quality, 'min_quality', errors, 0.01, 1);
		if (q !== null) minQuality = q;
	}
	if (Object.keys(errors).length) return fail(errors);
	return {
		ok: true,
		value: {
			type,
			tiers,
			...(hysteresis !== undefined ? { hysteresis_cm: hysteresis } : {}),
			cooldown_s: cooldownMin * 60,
			min_quality: minQuality,
			enabled: raw.enabled
		}
	};
}
