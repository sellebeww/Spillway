// Copyright (c) 2026 Vassel Goleyu. MIT License.
import { TILE_ATTRIBUTION, TILE_URL } from '$app/env/private';
import type { LayoutServerLoad } from './$types';

const DEFAULT_TILES = 'https://tile.openstreetmap.org/{z}/{x}/{y}.png';

/** TILE_URL must be an http(s) template with {z}, {x} and {y}; anything else falls back to the OSM demo tiles. */
function tileTemplate(raw: string | undefined): string {
	if (raw && /^https?:\/\//.test(raw) && ['{z}', '{x}', '{y}'].every((p) => raw.includes(p)))
		return raw;
	return DEFAULT_TILES;
}

export const load: LayoutServerLoad = ({ locals }) => ({
	signedIn: !!locals.session,
	who: locals.session?.label ?? null,
	tileUrl: tileTemplate(TILE_URL),
	attribution: TILE_ATTRIBUTION ?? '© OpenStreetMap contributors'
});
