// Copyright (c) 2026 Vassel Goleyu. MIT License.
import { error, fail, redirect } from '@sveltejs/kit';
import { messages } from '#lib/messages.ts';
import { adminApi } from '#lib/server/auth.ts';
import { describeFailure, formText, guard } from '#lib/server/admin.ts';
import type { Site } from '#lib/types.ts';
import { validateEmail, validateSite } from '#lib/validation.ts';
import type { Actions, PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ params, parent, url }) => {
	const { me, sites } = await parent();
	if (params.id === 'baru')
		return { site: null as Site | null, isAdmin: !!me?.admin, saved: false };
	const site = sites.find((s) => s.id === params.id);
	if (!site) error(404, messages.sites.notFound);
	return { site, isAdmin: !!me?.admin, saved: url.searchParams.get('saved') === '1' };
};

export const actions: Actions = {
	save: async (event) => {
		const fd = await event.request.formData();
		const values = {
			name: formText(fd, 'name'),
			slug: formText(fd, 'slug'),
			lat: formText(fd, 'lat'),
			lon: formText(fd, 'lon'),
			tz: formText(fd, 'tz'),
			is_public: fd.get('is_public') === 'on'
		};
		// The browser already checked this; the server checks again because the form can be posted by anything.
		const v = validateSite(values);
		if (!v.ok)
			return fail(400, { values, errors: v.errors, formError: messages.admin.fieldErrors });

		const id = event.params.id;
		const api = adminApi(event);
		const r =
			id === 'baru'
				? await api<Site>('/v1/sites', { method: 'POST', body: v.value })
				: await api<Site>(`/v1/sites/${id}`, { method: 'PUT', body: v.value });
		guard(event, r);
		if (!r.ok) {
			const slugTaken = r.status === 409;
			return fail(r.status || 502, {
				values,
				errors: slugTaken ? { slug: messages.sites.slugTaken } : {},
				formError: slugTaken
					? messages.admin.fieldErrors
					: r.status === 403
						? messages.admin.adminOnly
						: describeFailure(r)
			});
		}
		if (id === 'baru') redirect(303, `/admin/sites/${r.data.id}?saved=1`);
		return { values, errors: {}, saved: true };
	},

	operator: async (event) => {
		const fd = await event.request.formData();
		const raw = formText(fd, 'email');
		const v = validateEmail(raw);
		if (!v.ok) return fail(400, { operatorEmail: raw, operatorError: v.errors.email });
		const r = await adminApi(event)(`/v1/sites/${event.params.id}/operators`, {
			method: 'POST',
			body: { email: v.value }
		});
		guard(event, r);
		if (!r.ok) {
			return fail(r.status || 502, {
				operatorEmail: raw,
				operatorError:
					r.status === 404 && r.code === 'user_not_found'
						? messages.sites.operatorsHint
						: r.status === 403
							? messages.admin.adminOnly
							: describeFailure(r)
			});
		}
		return { operatorAdded: true };
	}
};
