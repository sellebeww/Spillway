<script lang="ts">
	import { enhance } from '$app/forms';
	import Empty from '#lib/components/Empty.svelte';
	import Field from '#lib/components/Field.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import { formatDateTime, formatNumber, utcToZonedLocal } from '#lib/format.ts';
	import { messages } from '#lib/messages.ts';
	import {
		isBackdated,
		parseNumber,
		previewLevel,
		validateCalibration,
		type Errors
	} from '#lib/validation.ts';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	const tz = $derived(data.chosen?.tz ?? 'Asia/Jakarta');
	const latestFrom = $derived(
		data.calibrations.length
			? Math.max(...data.calibrations.map((c) => Date.parse(c.valid_from)))
			: null
	);
	const nowMs = Date.now();
	const active = $derived(
		[...data.calibrations]
			.filter((c) => Date.parse(c.valid_from) <= nowMs)
			.sort(
				(a, b) => Date.parse(b.valid_from) - Date.parse(a.valid_from) || b.version - a.version
			)[0]
	);

	let offset = $state('');
	let min = $state('250');
	let max = $state('4500');
	let from = $state('');
	let note = $state('');
	let confirm = $state(false);
	let errors = $state<Errors>({});

	$effect(() => {
		const f = form && 'values' in form ? form.values : null;
		offset = f?.offset_cm ?? (active ? String(active.offset_cm) : '');
		min = f?.min_valid_mm ?? (active ? String(active.min_valid_mm) : '250');
		max = f?.max_valid_mm ?? (active ? String(active.max_valid_mm) : '4500');
		from = f?.valid_from ?? utcToZonedLocal(new Date(), tz);
		note = f?.note ?? '';
		confirm = f?.confirm_backdate ?? false;
		errors = (form && 'errors' in form && (form.errors as Errors)) || {};
	});

	const last = $derived(data.health?.last_reading ?? null);
	const offsetN = $derived(parseNumber(offset));
	const minN = $derived(parseNumber(min));
	const maxN = $derived(parseNumber(max));
	const before = $derived(last && active ? previewLevel(active.offset_cm, last.raw_mm) : null);
	const after = $derived(last && offsetN !== null ? previewLevel(offsetN, last.raw_mm) : null);
	const outOfRange = $derived(
		last && minN !== null && maxN !== null && (last.raw_mm < minN || last.raw_mm > maxN)
	);
	const backdated = $derived(isBackdated(from, tz, latestFrom));
	const formError = $derived(form && 'formError' in form ? (form.formError as string) : null);

	function versionState(c: { valid_from: string }) {
		if (c === active) return { text: messages.calibration.active, cls: 'ok', icon: 'check' };
		return Date.parse(c.valid_from) > nowMs
			? { text: messages.calibration.future, cls: 'warn', icon: 'clock' }
			: { text: messages.calibration.superseded, cls: 'idle', icon: 'flat' };
	}
</script>

<svelte:head
	><title>{messages.calibration.title} · {messages.admin.title} · {messages.appName}</title
	></svelte:head
>

<h1>{messages.calibration.title}</h1>
<p class="muted">{messages.calibration.intro}</p>

{#if !data.chosen}
	<Empty title={messages.calibration.noDevices} body={messages.devices.emptyHint}>
		<a class="btn" href="/admin/devices">{messages.nav.devices}</a>
	</Empty>
{:else}
	<form method="GET" class="row picker">
		<div class="field grow">
			<label for="device-select">{messages.calibration.pickDevice}</label>
			<select
				id="device-select"
				name="device"
				onchange={(e) => e.currentTarget.form?.requestSubmit()}
			>
				{#each data.options as o (o.id)}
					<option value={o.id} selected={o.id === data.chosen.id}>{o.siteName} / {o.name}</option>
				{/each}
			</select>
		</div>
		<noscript
			><button class="btn secondary" type="submit">{messages.admin.pickSiteGo}</button></noscript
		>
	</form>

	{#if data.saved}<p class="notice ok" role="status">
			<Icon name="check" size={20} />
			{messages.calibration.saved}
		</p>{/if}
	{#if data.loadError}<p class="notice bad" role="alert">{data.loadError}</p>{/if}

	<section class="card" aria-labelledby="hist-title">
		<h2 id="hist-title">{messages.calibration.history}</h2>
		{#if data.calibrations.length === 0}
			<p><strong>{messages.calibration.empty}</strong></p>
			<p class="muted">{messages.calibration.emptyHint}</p>
		{:else}
			<div class="table-wrap">
				<table>
					<thead>
						<tr>
							<th scope="col">{messages.calibration.colVersion}</th>
							<th scope="col">{messages.calibration.colOffset}</th>
							<th scope="col">{messages.calibration.colRange}</th>
							<th scope="col">{messages.calibration.colFrom}</th>
							<th scope="col">{messages.calibration.colNote}</th>
						</tr>
					</thead>
					<tbody>
						{#each [...data.calibrations].sort((a, b) => b.version - a.version) as c (c.version)}
							{@const s = versionState(c)}
							<tr>
								<th scope="row"
									>v{c.version}
									<span class="badge {s.cls}"><Icon name={s.icon} size={14} />{s.text}</span></th
								>
								<td>{formatNumber(c.offset_cm)}</td>
								<td>{c.min_valid_mm} - {c.max_valid_mm}</td>
								<td>{formatDateTime(c.valid_from, tz)}</td>
								<td>{c.note || '-'}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</section>

	<section class="card" aria-labelledby="new-title">
		<h2 id="new-title">{messages.calibration.newVersion}</h2>
		<p class="mono formula">{messages.calibration.formula}</p>
		{#if formError}<p class="notice bad" role="alert">
				<Icon name="alert" size={20} />
				{formError}
			</p>{/if}
		<form
			method="POST"
			action="?/add"
			class="stack"
			novalidate
			data-testid="calibration-form"
			use:enhance={({ cancel }) => {
				const v = validateCalibration(
					{
						offset_cm: offset,
						min_valid_mm: min,
						max_valid_mm: max,
						valid_from: from,
						note,
						confirm_backdate: confirm
					},
					{ tz, nowMs: Date.now(), latestFromMs: latestFrom }
				);
				if (!v.ok) {
					errors = v.errors;
					cancel();
					return;
				}
				errors = {};
				return async ({ update }) => update({ reset: false });
			}}
		>
			<input type="hidden" name="device" value={data.chosen.id} />
			<input type="hidden" name="tz" value={tz} />
			<input type="hidden" name="latest_from" value={latestFrom ?? ''} />
			<div class="form-grid">
				<Field
					id="offset"
					label={messages.calibration.fieldOffset}
					hint={messages.calibration.fieldOffsetHint}
					error={errors.offset_cm}
					required
				>
					{#snippet control(a)}<input
							{...a}
							type="text"
							name="offset_cm"
							bind:value={offset}
							inputmode="decimal"
							autocomplete="off"
						/>{/snippet}
				</Field>
				<Field
					id="from"
					label={messages.calibration.fieldFrom}
					hint={messages.calibration.fieldFromHint(tz)}
					error={errors.valid_from}
					required
				>
					{#snippet control(a)}<input
							{...a}
							type="datetime-local"
							name="valid_from"
							bind:value={from}
						/>{/snippet}
				</Field>
				<Field
					id="min"
					label={messages.calibration.fieldMin}
					hint={messages.calibration.fieldRangeHint}
					error={errors.min_valid_mm}
					required
				>
					{#snippet control(a)}<input
							{...a}
							type="text"
							name="min_valid_mm"
							bind:value={min}
							inputmode="numeric"
						/>{/snippet}
				</Field>
				<Field id="max" label={messages.calibration.fieldMax} error={errors.max_valid_mm} required>
					{#snippet control(a)}<input
							{...a}
							type="text"
							name="max_valid_mm"
							bind:value={max}
							inputmode="numeric"
						/>{/snippet}
				</Field>
			</div>
			<p class="muted hint">{messages.calibration.defaults}</p>
			<Field
				id="note"
				label={messages.calibration.fieldNote}
				hint={messages.calibration.fieldNoteHint}
				error={errors.note}
			>
				{#snippet control(a)}<textarea {...a} name="note" bind:value={note} maxlength="500"
					></textarea>{/snippet}
			</Field>

			{#if backdated || errors.confirm_backdate}
				<div class="notice warn" role="status">
					<Icon name="alert" size={20} />
					<div>
						<p>{messages.calibration.backdateNeeded}</p>
						<div class="check">
							<input
								id="confirm"
								type="checkbox"
								name="confirm_backdate"
								bind:checked={confirm}
								aria-invalid={errors.confirm_backdate ? true : undefined}
							/>
							<label for="confirm">{messages.calibration.confirmBackdate}</label>
						</div>
						{#if errors.confirm_backdate}<p class="err" role="alert">
								{errors.confirm_backdate}
							</p>{/if}
					</div>
				</div>
			{/if}

			<div class="preview" aria-live="polite" data-testid="preview">
				<h3>{messages.calibration.preview}</h3>
				{#if !last}
					<p class="muted">{messages.calibration.previewNone}</p>
				{:else if after === null}
					<p class="muted">{messages.calibration.previewInvalid}</p>
				{:else}
					<p class="muted">
						{messages.calibration.previewReading(String(last.raw_mm), formatDateTime(last.ts, tz))}
					</p>
					<div class="cmp">
						<div>
							<span class="lbl">{messages.calibration.previewBefore}</span><strong
								>{before === null ? '-' : `${formatNumber(before)} cm`}</strong
							>
						</div>
						<Icon name="flat" size={22} />
						<div>
							<span class="lbl">{messages.calibration.previewAfter}</span><strong class="new"
								>{formatNumber(after)} cm</strong
							>
						</div>
						{#if before !== null}
							<div>
								<span class="lbl">{messages.calibration.previewDelta}</span><strong
									>{after - before > 0 ? '+' : ''}{formatNumber(after - before)} cm</strong
								>
							</div>
						{/if}
					</div>
					{#if outOfRange}<p class="notice bad">
							<Icon name="alert" size={18} />
							{messages.calibration.previewOutOfRange}
						</p>{/if}
				{/if}
			</div>

			<div><button class="btn" type="submit">{messages.calibration.submit}</button></div>
		</form>
	</section>
{/if}

<style>
	h1 {
		margin-bottom: 0.25rem;
	}
	.picker {
		margin: 1rem 0;
	}
	.grow {
		flex: 1 1 18rem;
		max-width: 32rem;
	}
	section.card {
		margin-top: 1.25rem;
	}
	.formula {
		padding: 0.5rem 0.75rem;
		background: var(--surface-2);
		border-radius: var(--radius-sm);
		display: inline-block;
	}
	.hint,
	.err {
		margin: 0;
		font-size: 0.9rem;
	}
	.err {
		color: var(--bad);
		font-weight: 600;
	}
	.preview {
		padding: 1rem;
		border: 1.5px dashed var(--brand);
		border-radius: var(--radius-sm);
		background: color-mix(in srgb, var(--brand) 6%, var(--surface));
	}
	.preview h3 {
		margin: 0 0 0.4rem;
	}
	.cmp {
		display: flex;
		flex-wrap: wrap;
		gap: 0.75rem 1.5rem;
		align-items: center;
	}
	.cmp div {
		display: grid;
	}
	.lbl {
		font-size: 0.8rem;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		color: var(--ink-2);
		font-weight: 700;
	}
	.cmp strong {
		font-size: 1.5rem;
		font-variant-numeric: tabular-nums;
	}
	.new {
		color: var(--brand);
		animation: rise 0.4s var(--ease) both;
	}
</style>
