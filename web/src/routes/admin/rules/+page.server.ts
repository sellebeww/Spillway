// Copyright (c) 2026 Vassel Goleyu. MIT License.
import { fail, redirect } from '@sveltejs/kit';
import { messages } from '#lib/messages.ts';
import { adminApi } from '#lib/server/auth.ts';
import { describeFailure, formText, guard, pickSite } from '#lib/server/admin.ts';
import type { Paged, Rule } from '#lib/types.ts';
import { validateRule, type RuleValue } from '#lib/validation.ts';
import type { Actions, PageServerLoad } from './$types';

export const load: PageServerLoad = async (event) => {
	const { sites } = await event.parent();
	const site = pickSite(sites, event.url);
	if (!site)
		return { site: null, rules: [] as Rule[], loadError: null as string | null, saved: false };
	const r = await adminApi(event)<Paged<Rule>>(`/v1/sites/${site.id}/rules?limit=100`);
	guard(event, r);
	return {
		site,
		rules: r.ok ? r.data.items : [],
		loadError: r.ok ? null : describeFailure(r),
		saved: event.url.searchParams.get('saved') === '1'
	};
};

const body = (v: RuleValue) => ({
	type: v.type,
	tiers: v.tiers,
	...(v.hysteresis_cm !== undefined ? { hysteresis_cm: v.hysteresis_cm } : {}),
	cooldown_s: v.cooldown_s,
	min_quality: v.min_quality,
	enabled: v.enabled
});

export const actions: Actions = {
	save: async (event) => {
		const fd = await event.request.formData();
		const count = Math.min(3, Math.max(0, Number(formText(fd, 'tier_count')) || 0));
		const values = {
			type: formText(fd, 'type'),
			tiers: Array.from({ length: count }, (_, i) => ({
				value: formText(fd, `tier_value_${i}`),
				severity: formText(fd, `tier_severity_${i}`)
			})),
			hysteresis_cm: formText(fd, 'hysteresis_cm'),
			cooldown_min: formText(fd, 'cooldown_min'),
			min_quality: formText(fd, 'min_quality'),
			enabled: fd.get('enabled') === 'on'
		};
		const siteId = formText(fd, 'site');
		const ruleId = formText(fd, 'rule');
		const v = validateRule(values);
		if (!v.ok)
			return fail(400, { values, errors: v.errors, formError: messages.admin.fieldErrors, ruleId });
		const api = adminApi(event);
		const r = ruleId
			? await api(`/v1/rules/${ruleId}`, { method: 'PUT', body: body(v.value) })
			: await api(`/v1/sites/${siteId}/rules`, { method: 'POST', body: body(v.value) });
		guard(event, r);
		if (!r.ok) {
			return fail(r.status || 502, { values, errors: {}, formError: describeFailure(r), ruleId });
		}
		redirect(303, `/admin/rules?site=${encodeURIComponent(siteId)}&saved=1`);
	},

	toggle: async (event) => {
		const fd = await event.request.formData();
		const id = formText(fd, 'rule');
		const siteId = formText(fd, 'site');
		const api = adminApi(event);
		const cur = await api<Rule>(`/v1/rules/${id}`);
		guard(event, cur);
		if (!cur.ok) return fail(cur.status || 502, { formError: describeFailure(cur) });
		const c = cur.data;
		const r = await api(`/v1/rules/${id}`, {
			method: 'PUT',
			body: {
				type: c.type,
				tiers: c.tiers,
				...(c.type === 'THRESHOLD' ? { hysteresis_cm: c.hysteresis_cm } : {}),
				cooldown_s: c.cooldown_s,
				min_quality: c.min_quality,
				enabled: !c.enabled
			}
		});
		guard(event, r);
		if (!r.ok) return fail(r.status || 502, { formError: describeFailure(r) });
		redirect(303, `/admin/rules?site=${encodeURIComponent(siteId)}&saved=1`);
	}
};
