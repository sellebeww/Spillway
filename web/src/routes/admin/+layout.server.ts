// Copyright (c) 2026 Vassel Goleyu. MIT License.
import { adminApi } from '#lib/server/auth.ts';
import { describeFailure, guard } from '#lib/server/admin.ts';
import type { Me, Paged, Site } from '#lib/types.ts';
import type { LayoutServerLoad } from './$types';

// Every admin page needs to know who the caller is and which sites they may see. The API is the
// authority for both; the web app never decides roles by itself.
export const load: LayoutServerLoad = async (event) => {
	const api = adminApi(event);
	const [me, sites] = await Promise.all([
		api<Me>('/v1/me'),
		api<Paged<Site>>('/v1/sites?limit=200')
	]);
	guard(event, me);
	guard(event, sites);
	if (!me.ok)
		return { me: null, sites: [] as Site[], fatal: describeFailure(me), sitesError: null };
	return {
		me: me.data,
		sites: sites.ok ? sites.data.items : ([] as Site[]),
		fatal: null as string | null,
		sitesError: sites.ok ? null : describeFailure(sites)
	};
};
