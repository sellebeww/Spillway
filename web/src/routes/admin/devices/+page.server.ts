// Copyright (c) 2026 Vassel Goleyu. MIT License.
import { fail } from '@sveltejs/kit';
import { adminApi } from '#lib/server/auth.ts';
import { describeFailure, formText, guard, pickSite } from '#lib/server/admin.ts';
import type { Device, DeviceHealth, Paged } from '#lib/types.ts';
import { validateDevice } from '#lib/validation.ts';
import { messages } from '#lib/messages.ts';
import type { Actions, PageServerLoad } from './$types';

export const load: PageServerLoad = async (event) => {
	const { sites } = await event.parent();
	const site = pickSite(sites, event.url);
	if (!site) return { site: null, devices: [], loadError: null };
	const api = adminApi(event);
	const list = await api<Paged<Device>>(`/v1/sites/${site.id}/devices?limit=100`);
	guard(event, list);
	if (!list.ok) return { site, devices: [], loadError: describeFailure(list) };
	// Health is fetched per device in parallel; one failing device must not hide the others.
	const devices = await Promise.all(
		list.data.items.map(async (device) => {
			const h = await api<DeviceHealth>(`/v1/devices/${device.id}/health`);
			guard(event, h);
			return { device, health: h.ok ? h.data : null };
		})
	);
	return { site, devices, loadError: null };
};

export const actions: Actions = {
	register: async (event) => {
		const fd = await event.request.formData();
		const values = {
			device_key: formText(fd, 'device_key'),
			name: formText(fd, 'name'),
			expected_interval_s: formText(fd, 'expected_interval_s'),
			low_batt_v: formText(fd, 'low_batt_v')
		};
		const siteId = formText(fd, 'site');
		const v = validateDevice(values);
		if (!v.ok)
			return fail(400, { values, errors: v.errors, formError: messages.admin.fieldErrors });
		const r = await adminApi(event)<{ device: Device; secret: string }>(
			`/v1/sites/${siteId}/devices`,
			{
				method: 'POST',
				body: v.value
			}
		);
		guard(event, r);
		if (!r.ok) {
			const taken = r.status === 409;
			return fail(r.status || 502, {
				values,
				errors: taken ? { device_key: messages.devices.keyTaken } : {},
				formError: taken ? messages.admin.fieldErrors : describeFailure(r)
			});
		}
		// The secret is returned once, to this response only. It is not stored or logged here.
		return {
			secretFor: { name: r.data.device.name, secret: r.data.secret, kind: 'registered' as const }
		};
	},

	rotate: async (event) => {
		const fd = await event.request.formData();
		const id = formText(fd, 'device');
		const name = formText(fd, 'name');
		const r = await adminApi(event)<{ secret: string }>(`/v1/devices/${id}/rotate-secret`, {
			method: 'POST'
		});
		guard(event, r);
		if (!r.ok) return fail(r.status || 502, { formError: describeFailure(r) });
		return { secretFor: { name, secret: r.data.secret, kind: 'rotated' as const } };
	}
};
