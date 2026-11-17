// Copyright (c) 2026 Vassel Goleyu. MIT License.
import { publicApi, publicApiBase } from '#lib/server/auth.ts';
import type { PublicSiteCollection } from '#lib/types.ts';
import type { PageServerLoad } from './$types';

// Rendered on the server so the list of sites is in the HTML before any JavaScript (or the map) loads.
export const load: PageServerLoad = async (event) => {
	event.depends('app:map');
	const r = await publicApi(event)<PublicSiteCollection>('/v1/public/sites');
	return {
		sites: r.ok && Array.isArray(r.data?.features) ? r.data.features : null,
		apiBase: publicApiBase()
	};
};
