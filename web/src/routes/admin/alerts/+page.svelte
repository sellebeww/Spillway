<script lang="ts">
	import Empty from '#lib/components/Empty.svelte';
	import Pager from '#lib/components/Pager.svelte';
	import SeverityBadge from '#lib/components/SeverityBadge.svelte';
	import SiteSelect from '#lib/components/SiteSelect.svelte';
	import { formatDateTime, formatLevel, formatNumber } from '#lib/format.ts';
	import { messages } from '#lib/messages.ts';
	import type { AlertEvent } from '#lib/types.ts';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	const ruleName = (id: string) => {
		const r = data.rules.find((x) => x.id === id);
		return r
			? r.type === 'THRESHOLD'
				? messages.rules.typeTHRESHOLD
				: messages.rules.typeRATE_OF_RISE
			: messages.alerts.ruleUnknown;
	};
	const deviceName = (id: string | null) => data.devices.find((d) => d.id === id)?.name ?? '-';
	function duration(e: AlertEvent): string {
		const end = e.closed_at ? Date.parse(e.closed_at) : Date.now();
		const min = Math.max(0, Math.round((end - Date.parse(e.opened_at)) / 60000));
		return messages.alerts.durationFmt(Math.floor(min / 60), min % 60);
	}
</script>

<svelte:head
	><title>{messages.alerts.title} · {messages.admin.title} · {messages.appName}</title></svelte:head
>

<h1>{messages.alerts.title}</h1>
<p class="muted">{messages.alerts.intro}</p>

{#if !data.site}
	<Empty title={messages.admin.noSites} body={messages.admin.noSitesHint} />
{:else}
	<SiteSelect sites={data.sites} selected={data.site.id} />
	{#if data.loadError}
		<Empty icon="signal" tone="bad" title={data.loadError}
			><a class="btn" href="?site={data.site.id}">{messages.retry}</a></Empty
		>
	{:else if data.events.length === 0}
		<Empty title={messages.alerts.empty} body={messages.alerts.emptyHint} />
	{:else}
		<ul class="events">
			{#each data.events as e, i (e.id)}
				<li class="reveal" style="--i: {i}">
					<details class="card">
						<summary>
							<SeverityBadge severity={e.severity} quiet />
							<span class="badge" class:warn={e.state === 'OPEN'} class:idle={e.state !== 'OPEN'}>
								{e.state === 'OPEN' ? messages.alerts.stateOPEN : messages.alerts.stateCLOSED}
							</span>
							<span>{formatDateTime(e.opened_at, data.site.tz)}</span>
							{#if e.peak_level !== null}<span class="muted"
									>{messages.alerts.colPeak}: {formatLevel(e.peak_level)}</span
								>{/if}
							<span class="det">{messages.alerts.details}</span>
						</summary>
						<dl class="d">
							<div>
								<dt>{messages.alerts.detailRule}</dt>
								<dd>{ruleName(e.rule_id)}</dd>
							</div>
							<div>
								<dt>{messages.alerts.colOpened}</dt>
								<dd>{formatDateTime(e.opened_at, data.site.tz)}</dd>
							</div>
							<div>
								<dt>{messages.alerts.colClosed}</dt>
								<dd>
									{e.closed_at
										? formatDateTime(e.closed_at, data.site.tz)
										: messages.alerts.ongoing}
								</dd>
							</div>
							<div>
								<dt>{messages.alerts.detailDuration}</dt>
								<dd>{duration(e)}</dd>
							</div>
							<div>
								<dt>{messages.alerts.detailPeakLevel}</dt>
								<dd>{formatLevel(e.peak_level)}</dd>
							</div>
							<div>
								<dt>{messages.alerts.detailPeakRate}</dt>
								<dd>
									{e.peak_rate === null
										? '-'
										: `${formatNumber(e.peak_rate)} ${messages.units.cmPerHour}`}
								</dd>
							</div>
							<div>
								<dt>{messages.alerts.detailReminders}</dt>
								<dd>{e.reminder_count}</dd>
							</div>
							<div>
								<dt>{messages.alerts.detailDevice}</dt>
								<dd>{deviceName(e.device_id)}</dd>
							</div>
							<div>
								<dt>{messages.alerts.detailLastNotified}</dt>
								<dd>
									{e.last_notified_at ? formatDateTime(e.last_notified_at, data.site.tz) : '-'}
								</dd>
							</div>
						</dl>
					</details>
				</li>
			{/each}
		</ul>
		<Pager
			offset={data.offset}
			limit={data.limit}
			total={data.total}
			href={(o) => `?site=${data.site?.id}&offset=${o}`}
		/>
	{/if}
{/if}

<style>
	h1 {
		margin-bottom: 0.25rem;
	}
	.events {
		list-style: none;
		margin: 1rem 0 0;
		padding: 0;
		display: grid;
		gap: 0.75rem;
	}
	.events .card {
		margin-top: 0;
		padding: 0.5rem 1rem;
	}
	summary {
		display: flex;
		flex-wrap: wrap;
		gap: 0.4rem 0.8rem;
		align-items: center;
		min-height: 2.75rem;
		cursor: pointer;
	}
	.det {
		margin-left: auto;
		color: var(--brand);
		font-weight: 650;
	}
	.d {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
		gap: 0.75rem 1.5rem;
		margin: 0.75rem 0 0.5rem;
	}
	dt {
		font-size: 0.8rem;
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		color: var(--ink-2);
	}
	dd {
		margin: 0;
	}
</style>
