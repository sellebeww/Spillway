// Copyright (c) 2026 Vassel Goleyu. MIT License.
// Number and time formatting. All timestamps are UTC on the wire; display uses the site time zone.

import { messages } from './messages';

const numberFmt = new Intl.NumberFormat('id-ID', { maximumFractionDigits: 1 });

export function formatNumber(n: number, maxDigits = 1): string {
	return new Intl.NumberFormat('id-ID', { maximumFractionDigits: maxDigits }).format(n);
}

/** "123,4 cm", or a dash when there is no value. */
export function formatLevel(cm: number | null | undefined): string {
	if (cm === null || cm === undefined || !Number.isFinite(cm)) return '-';
	return `${numberFmt.format(cm)} ${messages.units.cm}`;
}

export function isValidTimeZone(tz: string): boolean {
	if (!tz || tz.length > 64) return false;
	try {
		new Intl.DateTimeFormat('en-US', { timeZone: tz });
		return true;
	} catch {
		return false;
	}
}

const INDONESIAN_ZONES: Record<string, string> = {
	'Asia/Jakarta': 'WIB',
	'Asia/Pontianak': 'WIB',
	'Asia/Makassar': 'WITA',
	'Asia/Jayapura': 'WIT'
};

/** WIB, WITA and WIT for Indonesian zones, otherwise the short name the platform gives (e.g. GMT+9). */
export function zoneAbbr(tz: string, at: Date): string {
	if (INDONESIAN_ZONES[tz]) return INDONESIAN_ZONES[tz];
	const part = new Intl.DateTimeFormat('en-US', { timeZone: tz, timeZoneName: 'short' })
		.formatToParts(at)
		.find((p) => p.type === 'timeZoneName');
	return part?.value ?? tz;
}

const EN_MONTHS = [
	'Jan',
	'Feb',
	'Mar',
	'Apr',
	'May',
	'Jun',
	'Jul',
	'Aug',
	'Sep',
	'Oct',
	'Nov',
	'Dec'
];
const ID_MONTHS = [
	'Jan',
	'Feb',
	'Mar',
	'Apr',
	'Mei',
	'Jun',
	'Jul',
	'Agu',
	'Sep',
	'Okt',
	'Nov',
	'Des'
];

function partsOf(date: Date, tz: string): Record<string, string> {
	const out: Record<string, string> = {};
	for (const p of new Intl.DateTimeFormat('en-GB', {
		timeZone: tz,
		year: 'numeric',
		month: 'short',
		day: 'numeric',
		hour: '2-digit',
		minute: '2-digit',
		hourCycle: 'h23'
	}).formatToParts(date)) {
		out[p.type] = p.value;
	}
	return out;
}

/** "16 Nov 2026 18.05 WIB" in the given zone. Invalid input gives a dash. */
export function formatDateTime(value: string | Date | null | undefined, tz: string): string {
	if (!value) return '-';
	const d = value instanceof Date ? value : new Date(value);
	if (Number.isNaN(d.getTime())) return '-';
	const safeTz = isValidTimeZone(tz) ? tz : 'Asia/Jakarta';
	const p = partsOf(d, safeTz);
	const month = ID_MONTHS[EN_MONTHS.indexOf(p.month.slice(0, 3))] ?? p.month;
	return `${Number(p.day)} ${month} ${p.year} ${p.hour}.${p.minute} ${zoneAbbr(safeTz, d)}`;
}

/** Relative age such as "3 menit lalu". */
export function formatAge(value: string | Date | null | undefined, nowMs: number): string {
	if (!value) return '-';
	const t = (value instanceof Date ? value : new Date(value)).getTime();
	if (Number.isNaN(t)) return '-';
	const s = Math.max(0, Math.round((nowMs - t) / 1000));
	if (s < 60) return 'baru saja';
	if (s < 3600) return `${Math.floor(s / 60)} menit lalu`;
	if (s < 86400) return `${Math.floor(s / 3600)} jam lalu`;
	return `${Math.floor(s / 86400)} hari lalu`;
}

function zonedFieldsAsUtc(date: Date, tz: string): number {
	const dtf = new Intl.DateTimeFormat('en-US', {
		timeZone: tz,
		hourCycle: 'h23',
		year: 'numeric',
		month: 'numeric',
		day: 'numeric',
		hour: 'numeric',
		minute: 'numeric',
		second: 'numeric'
	});
	const v: Record<string, number> = {};
	for (const p of dtf.formatToParts(date)) if (p.type !== 'literal') v[p.type] = Number(p.value);
	return Date.UTC(v.year, v.month - 1, v.day, v.hour, v.minute, v.second);
}

/** Offset of a zone from UTC at an instant, in minutes (WIB is +420). */
export function zoneOffsetMinutes(date: Date, tz: string): number {
	const truncated = Math.floor(date.getTime() / 1000) * 1000;
	return Math.round((zonedFieldsAsUtc(new Date(truncated), tz) - truncated) / 60000);
}

const LOCAL_RE = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/;

/** Converts a "YYYY-MM-DDTHH:mm" wall-clock time in a zone to the UTC instant, or null if invalid. */
export function zonedLocalToUtc(local: string, tz: string): Date | null {
	const m = LOCAL_RE.exec(local.trim());
	if (!m || !isValidTimeZone(tz)) return null;
	const [y, mo, d, h, mi] = m.slice(1).map(Number);
	const asUtc = Date.UTC(y, mo - 1, d, h, mi);
	const check = new Date(asUtc);
	if (check.getUTCMonth() !== mo - 1 || check.getUTCDate() !== d || h > 23 || mi > 59) return null;
	// Two passes handle zones whose offset changes between the guess and the answer (DST).
	let guess = asUtc - zoneOffsetMinutes(new Date(asUtc), tz) * 60000;
	guess = asUtc - zoneOffsetMinutes(new Date(guess), tz) * 60000;
	return new Date(guess);
}

/** "YYYY-MM-DDTHH:mm" wall-clock time of an instant in a zone, for <input type="datetime-local">. */
export function utcToZonedLocal(date: Date, tz: string): string {
	const ms = zonedFieldsAsUtc(new Date(Math.floor(date.getTime() / 60000) * 60000), tz);
	return new Date(ms).toISOString().slice(0, 16);
}
