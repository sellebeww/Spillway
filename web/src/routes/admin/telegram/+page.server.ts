// Copyright (c) 2026 Vassel Goleyu. MIT License.
import { fail } from '@sveltejs/kit';
import { messages } from '#lib/messages.ts';
import { adminApi } from '#lib/server/auth.ts';
import { describeFailure, formText, guard, pickSite } from '#lib/server/admin.ts';
import type { Channel, Paged } from '#lib/types.ts';
import type { Actions, PageServerLoad } from './$types';

export const load: PageServerLoad = async (event) => {
	const { sites } = await event.parent();
	const site = pickSite(sites, event.url);
	if (!site) return { site: null, channels: [] as Channel[], loadError: null as string | null };
	const r = await adminApi(event)<Paged<Channel>>(`/v1/sites/${site.id}/channels?limit=200`);
	guard(event, r);
	return { site, channels: r.ok ? r.data.items : [], loadError: r.ok ? null : describeFailure(r) };
};

export const actions: Actions = {
	link: async (event) => {
		const site = formText(await event.request.formData(), 'site');
		const r = await adminApi(event)<{ url: string; expires_at: string }>(
			`/v1/sites/${site}/subscription-links`,
			{ method: 'POST' }
		);
		guard(event, r);
		if (!r.ok) {
			return fail(r.status || 502, {
				formError: r.status === 409 ? messages.telegram.notConfigured : describeFailure(r)
			});
		}
		// Shown once, in this response only.
		return { link: { url: r.data.url, expiresAt: r.data.expires_at } };
	},

	revoke: async (event) => {
		const site = formText(await event.request.formData(), 'site');
		const r = await adminApi(event)<{ revoked: number }>(
			`/v1/sites/${site}/subscription-links/revoke`,
			{ method: 'POST' }
		);
		guard(event, r);
		if (!r.ok) return fail(r.status || 502, { formError: describeFailure(r) });
		return { revoked: r.data.revoked };
	}
};
