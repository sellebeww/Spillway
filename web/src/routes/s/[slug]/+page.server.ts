// Copyright (c) 2026 Vassel Goleyu. MIT License.
import { error } from '@sveltejs/kit';
import { messages } from '#lib/messages.ts';
import { publicApi, publicApiBase } from '#lib/server/auth.ts';
import { safeTelegramUrl } from '#lib/server/security.ts';
import type { PublicSite } from '#lib/types.ts';
import type { PageServerLoad } from './$types';

const SLUG_RE = /^[a-z0-9][a-z0-9-]{1,62}$/;

// Server-rendered: the level, status and the DATA TERTUNDA label are in the HTML before any script runs.
export const load: PageServerLoad = async (event) => {
	const slug = event.params.slug;
	if (!SLUG_RE.test(slug)) error(404, messages.site.notFoundBody);
	event.depends('app:site');
	const r = await publicApi(event)<PublicSite>(`/v1/public/sites/${slug}`);
	if (!r.ok) {
		if (r.status === 404) error(404, messages.site.notFoundBody);
		if (r.status === 429) error(429, messages.admin.genericError);
		error(503, messages.admin.apiUnreachable);
	}
	const site: PublicSite = {
		...r.data,
		thresholds: r.data.thresholds ?? [],
		alerts: r.data.alerts ?? [],
		devices: r.data.devices ?? [],
		telegram_url: safeTelegramUrl(r.data.telegram_url)
	};
	return { site, apiBase: publicApiBase() };
};
