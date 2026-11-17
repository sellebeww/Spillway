// Copyright (c) 2026 Vassel Goleyu. MIT License.
import { adminApi } from '#lib/server/auth.ts';
import { describeFailure, guard, pageOffset } from '#lib/server/admin.ts';
import type { AuditEntry, Paged } from '#lib/types.ts';
import type { PageServerLoad } from './$types';

const LIMIT = 50;

export const load: PageServerLoad = async (event) => {
	const offset = pageOffset(event.url);
	const r = await adminApi(event)<Paged<AuditEntry>>(`/v1/audit?limit=${LIMIT}&offset=${offset}`);
	guard(event, r);
	return {
		entries: r.ok ? r.data.items : [],
		total: r.ok ? r.data.total : 0,
		offset,
		limit: LIMIT,
		forbidden: !r.ok && r.status === 403,
		loadError: r.ok || r.status === 403 ? null : describeFailure(r)
	};
};
