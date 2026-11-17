// Copyright (c) 2026 Vassel Goleyu. MIT License.
// Shapes of the Spillway API (docs/openapi.yaml). Only fields the UI uses are listed.

export interface Notice {
	notice?: string;
}

export interface PublicSiteProps {
	slug: string;
	name: string;
	tz: string;
	level_cm: number | null;
	status: 'ONLINE' | 'DELAYED' | 'OFFLINE';
	updated_at: string | null;
	stale: boolean;
	stale_label: string | null;
}

export interface PublicSiteFeature {
	type: 'Feature';
	geometry: { type: 'Point'; coordinates: [number, number] }; // [lon, lat]
	properties: PublicSiteProps;
}

export interface PublicSiteCollection {
	type: 'FeatureCollection';
	features: PublicSiteFeature[];
}

export interface PublicDevice {
	id: string;
	name: string;
	level_cm: number | null;
	status: 'ONLINE' | 'DELAYED' | 'OFFLINE';
	updated_at: string | null;
	stale: boolean;
	stale_label: string | null;
}

export interface PublicThreshold {
	level_cm: number;
	severity: 1 | 2 | 3;
}

export interface PublicAlert {
	severity: 1 | 2 | 3;
	state: 'OPEN' | 'CLOSED';
	opened_at: string;
	closed_at: string | null;
	peak_level_cm: number | null;
}

export interface PublicSite extends Notice {
	slug: string;
	name: string;
	lat: number;
	lon: number;
	tz: string;
	level_cm: number | null;
	status: 'ONLINE' | 'DELAYED' | 'OFFLINE';
	updated_at: string | null;
	stale: boolean;
	stale_label: string | null;
	severity: 0 | 1 | 2 | 3;
	thresholds: PublicThreshold[];
	alerts: PublicAlert[];
	devices: PublicDevice[];
	/** Optional single-use Telegram deep link, when the operator publishes one for this site. */
	telegram_url?: string;
}

export interface LiveReading {
	site: string;
	device_id: string;
	ts: number; // epoch seconds
	level_cm: number;
	late: boolean;
}

// ---- authenticated ----

export interface Me {
	user_id: string;
	email: string | null;
	admin: boolean;
	site_ids: string[];
}

export interface Site {
	id: string;
	slug: string;
	name: string;
	lat: number;
	lon: number;
	tz: string;
	is_public: boolean;
	created_at: string;
	updated_at: string;
}

export interface Device {
	id: string;
	site_id: string;
	device_key: string;
	name: string;
	expected_interval_s: number;
	low_batt_v: number;
	last_seen_at: string | null;
	last_status: 'ONLINE' | 'DELAYED' | 'OFFLINE';
	created_at: string;
}

export interface DeviceHealth {
	status: 'ONLINE' | 'DELAYED' | 'OFFLINE';
	last_seen_at: string | null;
	stale: boolean;
	health_score: number;
	readings_last_hour: number;
	battery_slope_v_per_day: number | null;
	last_reading: {
		ts: string;
		raw_mm: number;
		level_cm: number;
		batt_v: number | null;
		quality_score: number;
	} | null;
	evaluated_at: string;
}

export interface Calibration {
	version: number;
	offset_cm: number;
	min_valid_mm: number;
	max_valid_mm: number;
	valid_from: string;
	note: string;
}

export interface Rule {
	id: string;
	site_id: string;
	type: 'THRESHOLD' | 'RATE_OF_RISE';
	tiers: Array<{ value: number; severity: number }>;
	hysteresis_cm: number;
	cooldown_s: number;
	min_quality: number;
	enabled: boolean;
	created_at: string;
	updated_at: string;
}

export interface AlertEvent {
	id: string;
	rule_id: string;
	state: 'OPEN' | 'CLOSED';
	severity: 1 | 2 | 3;
	opened_at: string;
	closed_at: string | null;
	peak_level: number | null;
	peak_rate: number | null;
	last_notified_at: string | null;
	device_id: string | null;
	reminder_count: number;
}

export interface Channel {
	id: string;
	kind: string;
	chat_hint: string;
	created_at: string;
}

export interface AuditEntry {
	id: number;
	at: string;
	actor: string;
	action: string;
	entity_type: string;
	entity_id: string;
	before: unknown;
	after: unknown;
}

export interface Paged<T> {
	items: T[];
	limit: number;
	offset: number;
	total: number;
}
