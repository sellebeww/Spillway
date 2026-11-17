<script lang="ts">
	import { enhance } from '$app/forms';
	import Empty from '#lib/components/Empty.svelte';
	import Field from '#lib/components/Field.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import SecretReveal from '#lib/components/SecretReveal.svelte';
	import SiteSelect from '#lib/components/SiteSelect.svelte';
	import StatusBadge from '#lib/components/StatusBadge.svelte';
	import { formatAge, formatDateTime, formatLevel, formatNumber } from '#lib/format.ts';
	import { messages } from '#lib/messages.ts';
	import { validateDevice, type Errors } from '#lib/validation.ts';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	let key = $state('');
	let name = $state('');
	let interval = $state('60');
	let lowBatt = $state('3.3');
	let errors = $state<Errors>({});
	let shownSecret = $state<{ name: string; secret: string; kind: 'registered' | 'rotated' } | null>(
		null
	);
	const now = Date.now();

	$effect(() => {
		if (form && 'secretFor' in form && form.secretFor) {
			shownSecret = form.secretFor;
			key = name = '';
		}
		errors = (form && 'errors' in form && (form.errors as Errors)) || {};
		if (form && 'values' in form && form.values) {
			({ device_key: key, name, expected_interval_s: interval, low_batt_v: lowBatt } = form.values);
		}
	});

	const formError = $derived(form && 'formError' in form ? (form.formError as string) : null);

	function batteryText(slope: number | null) {
		if (slope === null)
			return { icon: 'flat', text: messages.devices.batteryUnknown, tone: 'idle' };
		if (slope < -0.02)
			return {
				icon: 'down',
				text: `${messages.devices.batteryFalling} (${formatNumber(slope, 3)} ${messages.units.voltPerDay})`,
				tone: 'warn'
			};
		if (slope > 0.02)
			return {
				icon: 'up',
				text: `${messages.devices.batteryRising} (+${formatNumber(slope, 3)} ${messages.units.voltPerDay})`,
				tone: 'ok'
			};
		return {
			icon: 'flat',
			text: `${messages.devices.batterySteady} (${formatNumber(slope, 3)} ${messages.units.voltPerDay})`,
			tone: 'ok'
		};
	}
</script>

<svelte:head
	><title>{messages.devices.title} · {messages.admin.title} · {messages.appName}</title
	></svelte:head
>

<h1>{messages.devices.title}</h1>
<p class="muted">{messages.devices.intro}</p>

{#if data.site === null}
	<Empty title={messages.admin.noSites} body={messages.admin.noSitesHint} />
{:else}
	<SiteSelect sites={data.sites} selected={data.site.id} />

	{#if shownSecret}
		<SecretReveal
			title={messages.devices.secretTitle}
			subtitle={messages.devices.secretFor(shownSecret.name)}
			value={shownSecret.secret}
			warning={messages.devices.secretWarn}
			ondismiss={() => (shownSecret = null)}
		/>
	{/if}
	{#if formError}<p class="notice bad" role="alert">
			<Icon name="alert" size={20} />
			{formError}
		</p>{/if}

	{#if data.loadError}
		<Empty icon="signal" tone="bad" title={data.loadError}
			><a class="btn" href="?site={data.site.id}">{messages.retry}</a></Empty
		>
	{:else if data.devices.length === 0}
		<Empty title={messages.devices.empty} body={messages.devices.emptyHint} />
	{:else}
		<ul class="devs">
			{#each data.devices as { device, health }, i (device.id)}
				<li class="card dev reveal" style="--i: {i}">
					<div class="row between">
						<div>
							<h2>{device.name}</h2>
							<p class="mono muted">{device.device_key}</p>
						</div>
						<StatusBadge status={health?.status ?? device.last_status} />
					</div>
					{#if health}
						<dl class="stats">
							<div>
								<dt>{messages.devices.healthScore}</dt>
								<dd>
									<span
										class="meter"
										role="meter"
										aria-valuemin="0"
										aria-valuemax="100"
										aria-valuenow={Math.round(health.health_score * 100)}
										aria-label={messages.devices.healthScore}
									>
										<span class="fill" style="width: {Math.round(health.health_score * 100)}%"
										></span>
									</span>
									<strong>{Math.round(health.health_score * 100)}%</strong>
								</dd>
								<dd class="muted tiny">
									{messages.devices.readingsLastHour}: {health.readings_last_hour}
								</dd>
							</div>
							<div>
								<dt>{messages.devices.battery}</dt>
								<dd>
									{#each [batteryText(health.battery_slope_v_per_day)] as b (b.text)}
										<span class="badge {b.tone}"><Icon name={b.icon} size={14} />{b.text}</span>
									{/each}
								</dd>
								<dd class="muted tiny">
									{messages.devices.fieldLowBatt}: {device.low_batt_v}
									{messages.units.volt}
								</dd>
							</div>
							<div>
								<dt>{messages.devices.lastReading}</dt>
								<dd>
									{#if health.last_reading}
										<strong>{formatLevel(health.last_reading.level_cm)}</strong>
										<span class="muted tiny">
											{messages.devices.rawValue}: {health.last_reading.raw_mm}
											{messages.units.mm}
											{#if health.last_reading.batt_v !== null}· {health.last_reading.batt_v}
												{messages.units.volt}{/if}
										</span>
										<span class="muted tiny"
											>{formatDateTime(health.last_reading.ts, data.site.tz)} ({formatAge(
												health.last_reading.ts,
												now
											)})</span
										>
									{:else}
										<span class="muted">{messages.devices.lastReadingNone}</span>
									{/if}
								</dd>
							</div>
							<div>
								<dt>{messages.devices.interval}</dt>
								<dd>{device.expected_interval_s} {messages.units.seconds}</dd>
								<dd class="muted tiny">
									{messages.devices.lastSeen}: {health.last_seen_at
										? formatAge(health.last_seen_at, now)
										: '-'}
								</dd>
							</div>
						</dl>
					{:else}
						<p class="notice warn">{messages.devices.healthError}</p>
					{/if}
					<div class="row">
						<a class="btn secondary small" href="/admin/calibration?device={device.id}"
							>{messages.devices.calibrate}</a
						>
						<form
							method="POST"
							action="?/rotate"
							use:enhance={({ cancel }) => {
								if (!confirm(messages.devices.rotateConfirm)) cancel();
								return async ({ update }) => update({ reset: false });
							}}
						>
							<input type="hidden" name="device" value={device.id} />
							<input type="hidden" name="name" value={device.name} />
							<button class="btn secondary small" type="submit">{messages.devices.rotate}</button>
						</form>
					</div>
				</li>
			{/each}
		</ul>
	{/if}

	<section class="card" aria-labelledby="reg-title">
		<h2 id="reg-title">{messages.devices.register}</h2>
		<form
			method="POST"
			action="?/register"
			class="stack"
			novalidate
			use:enhance={({ cancel }) => {
				const v = validateDevice({
					device_key: key,
					name,
					expected_interval_s: interval,
					low_batt_v: lowBatt
				});
				if (!v.ok) {
					errors = v.errors;
					cancel();
					return;
				}
				errors = {};
				return async ({ update }) => update({ reset: false });
			}}
		>
			<input type="hidden" name="site" value={data.site.id} />
			<div class="form-grid">
				<Field
					id="dkey"
					label={messages.devices.fieldKey}
					hint={messages.devices.fieldKeyHint}
					error={errors.device_key}
					required
				>
					{#snippet control(a)}<input
							{...a}
							type="text"
							name="device_key"
							bind:value={key}
							maxlength="64"
							autocomplete="off"
							spellcheck="false"
						/>{/snippet}
				</Field>
				<Field id="dname" label={messages.devices.fieldName} error={errors.name} required>
					{#snippet control(a)}<input
							{...a}
							type="text"
							name="name"
							bind:value={name}
							maxlength="120"
							autocomplete="off"
						/>{/snippet}
				</Field>
				<Field
					id="dint"
					label={messages.devices.fieldInterval}
					hint={messages.devices.fieldIntervalHint}
					error={errors.expected_interval_s}
					required
				>
					{#snippet control(a)}<input
							{...a}
							type="text"
							name="expected_interval_s"
							bind:value={interval}
							inputmode="numeric"
						/>{/snippet}
				</Field>
				<Field
					id="dbatt"
					label={messages.devices.fieldLowBatt}
					hint={messages.devices.fieldLowBattHint}
					error={errors.low_batt_v}
					required
				>
					{#snippet control(a)}<input
							{...a}
							type="text"
							name="low_batt_v"
							bind:value={lowBatt}
							inputmode="decimal"
						/>{/snippet}
				</Field>
			</div>
			<div>
				<button class="btn" type="submit"
					><Icon name="plus" size={18} />{messages.devices.register}</button
				>
			</div>
		</form>
	</section>
{/if}

<style>
	h1 {
		margin-bottom: 0.25rem;
	}
	.devs {
		list-style: none;
		margin: 1.25rem 0;
		padding: 0;
		display: grid;
		gap: 1rem;
	}
	.card + .card,
	.devs .card {
		margin-top: 0;
	}
	h2 {
		margin: 0;
		font-size: 1.15rem;
	}
	.dev p {
		margin: 0.1rem 0 0;
	}
	.stats {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(13rem, 1fr));
		gap: 1rem;
		margin: 1rem 0;
	}
	.stats dt {
		font-size: 0.8rem;
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		color: var(--ink-2);
		margin-bottom: 0.25rem;
	}
	.stats dd {
		margin: 0;
		display: flex;
		flex-wrap: wrap;
		gap: 0.2rem 0.6rem;
		align-items: center;
	}
	.tiny {
		font-size: 0.85rem;
	}
	.meter {
		flex: 1 1 6rem;
		height: 0.8rem;
		border-radius: 999px;
		background: var(--surface-2);
		border: 1.5px solid var(--ink-2);
		overflow: hidden;
	}
	.fill {
		display: block;
		height: 100%;
		background: linear-gradient(90deg, var(--brand), var(--ok));
		transition: width 0.9s var(--ease);
		animation: grow 0.9s var(--ease) both;
	}
	@keyframes grow {
		from {
			width: 0;
		}
	}
	section.card {
		margin-top: 1.25rem;
	}
</style>
