// Copyright (c) 2026 Vassel Goleyu. MIT License.
import { adminApi } from '#lib/server/auth.ts';
import { describeFailure, guard, pageOffset, pickSite } from '#lib/server/admin.ts';
import type { AlertEvent, Device, Paged, Rule } from '#lib/types.ts';
import type { PageServerLoad } from './$types';

const LIMIT = 25;

export const load: PageServerLoad = async (event) => {
	const { sites } = await event.parent();
	const site = pickSite(sites, event.url);
	const offset = pageOffset(event.url);
	if (!site)
		return {
			site: null,
			events: [] as AlertEvent[],
			total: 0,
			offset,
			limit: LIMIT,
			rules: [] as Rule[],
			devices: [] as Device[],
			loadError: null as string | null
		};
	const api = adminApi(event);
	const [ev, rules, devs] = await Promise.all([
		api<Paged<AlertEvent>>(`/v1/sites/${site.id}/alerts?limit=${LIMIT}&offset=${offset}`),
		api<Paged<Rule>>(`/v1/sites/${site.id}/rules?limit=100`),
		api<Paged<Device>>(`/v1/sites/${site.id}/devices?limit=100`)
	]);
	guard(event, ev);
	return {
		site,
		events: ev.ok ? ev.data.items : [],
		total: ev.ok ? ev.data.total : 0,
		offset,
		limit: LIMIT,
		rules: rules.ok ? rules.data.items : [],
		devices: devs.ok ? devs.data.items : [],
		loadError: ev.ok ? null : describeFailure(ev)
	};
};
