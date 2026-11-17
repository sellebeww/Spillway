// Copyright (c) 2026 Vassel Goleyu. MIT License.
import { fail, redirect } from '@sveltejs/kit';
import { messages } from '#lib/messages.ts';
import { adminApi } from '#lib/server/auth.ts';
import { describeFailure, formText, guard } from '#lib/server/admin.ts';
import { isValidTimeZone } from '#lib/format.ts';
import type { Calibration, Device, DeviceHealth, Paged } from '#lib/types.ts';
import { validateCalibration } from '#lib/validation.ts';
import type { Actions, PageServerLoad } from './$types';

export const load: PageServerLoad = async (event) => {
	const { sites } = await event.parent();
	const api = adminApi(event);
	const lists = await Promise.all(
		sites.slice(0, 40).map(async (site) => {
			const r = await api<Paged<Device>>(`/v1/sites/${site.id}/devices?limit=100`);
			guard(event, r);
			return r.ok
				? r.data.items.map((d) => ({ id: d.id, name: d.name, siteName: site.name, tz: site.tz }))
				: [];
		})
	);
	const options = lists.flat();
	const chosen =
		options.find((o) => o.id === event.url.searchParams.get('device')) ?? options[0] ?? null;
	if (!chosen)
		return {
			options,
			chosen: null,
			calibrations: [] as Calibration[],
			health: null as DeviceHealth | null,
			loadError: null as string | null,
			saved: false
		};

	const [cal, health] = await Promise.all([
		api<Paged<Calibration>>(`/v1/devices/${chosen.id}/calibrations?limit=200`),
		api<DeviceHealth>(`/v1/devices/${chosen.id}/health`)
	]);
	guard(event, cal);
	guard(event, health);
	return {
		options,
		chosen,
		calibrations: cal.ok ? cal.data.items : [],
		health: health.ok ? health.data : null,
		loadError: cal.ok ? null : describeFailure(cal),
		saved: event.url.searchParams.get('saved') === '1'
	};
};

export const actions: Actions = {
	add: async (event) => {
		const fd = await event.request.formData();
		const values = {
			offset_cm: formText(fd, 'offset_cm'),
			min_valid_mm: formText(fd, 'min_valid_mm'),
			max_valid_mm: formText(fd, 'max_valid_mm'),
			valid_from: formText(fd, 'valid_from'),
			note: formText(fd, 'note'),
			confirm_backdate: fd.get('confirm_backdate') === 'on'
		};
		const device = formText(fd, 'device');
		const tz = formText(fd, 'tz');
		const latest = Number(formText(fd, 'latest_from'));
		// tz and latest_from only steer the early checks; the API enforces append-only and backdating itself.
		const v = validateCalibration(values, {
			tz: isValidTimeZone(tz) ? tz : 'Asia/Jakarta',
			nowMs: Date.now(),
			latestFromMs: Number.isFinite(latest) && latest > 0 ? latest : null
		});
		if (!v.ok)
			return fail(400, { values, errors: v.errors, formError: messages.admin.fieldErrors });
		const r = await adminApi(event)(`/v1/devices/${device}/calibrations`, {
			method: 'POST',
			body: v.value
		});
		guard(event, r);
		if (!r.ok) {
			const backdated = r.status === 409 && r.code === 'backdated';
			return fail(r.status || 502, {
				values,
				errors: backdated ? { confirm_backdate: messages.calibration.backdateNeeded } : {},
				formError: backdated ? messages.admin.fieldErrors : describeFailure(r)
			});
		}
		redirect(303, `/admin/calibration?device=${encodeURIComponent(device)}&saved=1`);
	}
};
