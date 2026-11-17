// Copyright (c) 2026 Vassel Goleyu. MIT License.
// Helpers shared by the admin pages' load functions and form actions.
import { redirect, type RequestEvent } from '@sveltejs/kit';
import { messages } from '#lib/messages.ts';
import type { Site } from '#lib/types.ts';
import type { ApiResult } from './api';

type Failed = Extract<ApiResult<unknown>, { ok: false }>;

/** A rejected token (401) ends the session: send the user to sign in and come back afterwards. */
export function guard<T>(event: RequestEvent, r: ApiResult<T>): void {
	if (!r.ok && r.status === 401) {
		redirect(303, `/auth/login?next=${encodeURIComponent(event.url.pathname + event.url.search)}`);
	}
}

/** User-facing text for a failed API call. The API's own English message is never shown. */
export function describeFailure(r: Failed): string {
	if (r.status === 0 || r.status >= 500) return messages.admin.apiUnreachable;
	if (r.status === 403) return messages.admin.forbidden;
	if (r.status === 404) return messages.sites.notFound;
	if (r.status === 409) return messages.admin.conflict;
	return messages.admin.genericError;
}

/** The site named by ?site=, else the first one the caller may see. */
export function pickSite(sites: Site[], url: URL): Site | null {
	const id = url.searchParams.get('site');
	return sites.find((s) => s.id === id) ?? sites[0] ?? null;
}

export const formText = (fd: FormData, key: string): string => {
	const v = fd.get(key);
	return typeof v === 'string' ? v : '';
};

export function pageOffset(url: URL): number {
	const n = Number(url.searchParams.get('offset'));
	return Number.isInteger(n) && n >= 0 && n < 100_000 ? n : 0;
}
