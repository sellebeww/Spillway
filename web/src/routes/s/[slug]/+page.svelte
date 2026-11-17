<script lang="ts">
	import { onMount } from 'svelte';
	import { invalidate } from '$app/navigation';
	import Chart from '#lib/components/Chart.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import LevelGauge from '#lib/components/LevelGauge.svelte';
	import SeverityBadge from '#lib/components/SeverityBadge.svelte';
	import StaleBanner from '#lib/components/StaleBanner.svelte';
	import StatusBadge from '#lib/components/StatusBadge.svelte';
	import { formatAge, formatDateTime, formatLevel, formatNumber, zoneAbbr } from '#lib/format.ts';
	import { openLive, type LiveState } from '#lib/live.ts';
	import { messages, type Severity } from '#lib/messages.ts';
	import {
		isResolution,
		rangeWindow,
		RANGE_IDS,
		type RangeId,
		type Resolution
	} from '#lib/resolution.ts';
	import {
		appendPoint,
		riseRateCmPerHour,
		toChartPoints,
		trendOf,
		type ChartPoint,
		type SeriesResponse
	} from '#lib/series.ts';
	import { deriveFreshness, isFreshReading } from '#lib/stale.ts';
	import type { LiveReading } from '#lib/types.ts';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();
	const site = $derived(data.site);
	const slug = $derived(site.slug);

	let range = $state<RangeId>('24h');
	let series = $state<{
		points: ChartPoint[];
		res: Resolution;
		fromMs: number;
		toMs: number;
		truncated: boolean;
		deviceId: string;
	} | null>(null);
	let seriesState = $state<'loading' | 'ok' | 'error'>('loading');
	let ratePoints = $state<ChartPoint[]>([]);
	let rateDevice = $state<string | null>(null);

	let live = $state<{ level: number; ts: number } | null>(null);
	let liveAt = $state<number | null>(null);
	let lastOk = $state(Date.now());
	let now = $state(Date.now());
	let liveState = $state<LiveState>('connecting');

	// A fresh server answer replaces whatever live readings patched in.
	$effect(() => {
		void data.site;
		live = null;
		liveAt = null;
		lastOk = Date.now();
	});

	const level = $derived(live ? live.level : site.level_cm);
	const updatedAt = $derived(live ? new Date(live.ts).toISOString() : site.updated_at);
	const fresh = $derived(
		deriveFreshness({ apiStale: site.stale, lastOkMs: lastOk, nowMs: now, liveReadingAtMs: liveAt })
	);
	const rate = $derived(riseRateCmPerHour(ratePoints, now));
	const trend = $derived(rate === null ? null : trendOf(rate));
	const sev = $derived((site.severity >= 0 && site.severity <= 3 ? site.severity : 0) as Severity);

	async function getSeries(query: string, signal: AbortSignal): Promise<SeriesResponse> {
		const res = await fetch(
			`${data.apiBase.replace(/\/$/, '')}/v1/public/sites/${encodeURIComponent(slug)}/series?${query}`,
			{
				signal,
				headers: { accept: 'application/json' }
			}
		);
		if (!res.ok) throw new Error(String(res.status));
		const body = (await res.json()) as SeriesResponse;
		if (!isResolution(body.res) || !Array.isArray(body.points)) throw new Error('bad series');
		return body;
	}

	async function loadSeries(r: RangeId, signal: AbortSignal, quiet: boolean) {
		if (!quiet) seriesState = 'loading';
		const w = rangeWindow(r, Date.now());
		try {
			const body = await getSeries(
				new URLSearchParams({ from: w.from, to: w.to, res: 'auto' }).toString(),
				signal
			);
			series = {
				points: toChartPoints(body),
				res: body.res,
				fromMs: Date.parse(w.from),
				toMs: Date.parse(w.to),
				truncated: body.truncated === true,
				deviceId: body.device_id
			};
			seriesState = 'ok';
		} catch (e) {
			if ((e as Error).name === 'AbortError') return;
			if (!quiet) seriesState = 'error';
		}
	}

	async function loadRate(signal: AbortSignal) {
		const t = Date.now();
		try {
			const body = await getSeries(
				new URLSearchParams({
					from: new Date(t - 11 * 60_000).toISOString(),
					to: new Date(t + 60_000).toISOString(),
					res: 'raw'
				}).toString(),
				signal
			);
			ratePoints = toChartPoints(body);
			rateDevice = body.device_id;
		} catch {
			/* the rate simply stays unknown; the level and the stale label do not depend on it */
		}
	}

	// Range selection: refetch whenever it changes; abort an older request still in flight.
	$effect(() => {
		const r = range;
		const ac = new AbortController();
		loadSeries(r, ac.signal, false);
		const t = setInterval(() => loadSeries(r, ac.signal, true), 60_000);
		return () => {
			ac.abort();
			clearInterval(t);
		};
	});

	function onReading(r: LiveReading) {
		const ts = r.ts * 1000;
		// Fail-visible: only a reading that is recent by its own timestamp may clear a stale label.
		if (r.late || r.site !== slug || !isFreshReading(ts, Date.now())) return;
		if (!live || ts >= live.ts) {
			live = { level: r.level_cm, ts };
			liveAt = Date.now();
		}
		if (r.device_id === rateDevice)
			ratePoints = appendPoint(ratePoints, { t: ts, v: r.level_cm }, 400);
		if (series && series.res === 'raw' && r.device_id === series.deviceId) {
			series = {
				...series,
				points: appendPoint(series.points, { t: ts, v: r.level_cm }),
				toMs: Math.max(series.toMs, ts)
			};
		}
	}

	onMount(() => {
		const handle = openLive(data.apiBase, slug, { onReading, onState: (s) => (liveState = s) });
		const ac = new AbortController();
		loadRate(ac.signal);
		const tick = setInterval(() => (now = Date.now()), 15_000);
		const rateTimer = setInterval(() => loadRate(ac.signal), 60_000);
		const poll = setInterval(() => {
			if (!document.hidden) invalidate('app:site');
		}, 30_000);
		const onVisible = () => {
			if (!document.hidden) invalidate('app:site');
		};
		document.addEventListener('visibilitychange', onVisible);
		return () => {
			handle.close();
			ac.abort();
			clearInterval(tick);
			clearInterval(rateTimer);
			clearInterval(poll);
			document.removeEventListener('visibilitychange', onVisible);
		};
	});

	const tableRows = $derived(series ? series.points.slice(-12).reverse() : []);
</script>

<svelte:head>
	<title>{site.name} · {messages.appName}</title>
	<meta name="description" content="{messages.site.currentLevel}: {site.name}" />
</svelte:head>

<div class="container page">
	<p class="crumb"><a href="/"><Icon name="pin" size={16} /> {messages.site.backToMap}</a></p>

	<div class="head reveal">
		<h1>{site.name}</h1>
		<p class="muted">
			{messages.site.zone}: {site.tz} ({zoneAbbr(site.tz, new Date(now))})
			<span class="livepill" class:on={liveState === 'open'} role="status">
				<span class="dot" aria-hidden="true"></span>
				{liveState === 'open' ? messages.site.connectionLive : messages.site.connectionReconnecting}
			</span>
		</p>
	</div>

	{#if fresh.stale}
		<StaleBanner
			updatedText={updatedAt ? formatDateTime(updatedAt, site.tz) : null}
			connectionLost={fresh.connectionLost}
		/>
	{/if}

	<section class="card hero reveal" style="--i: 1" aria-labelledby="level-title">
		<h2 id="level-title" class="sr-only">{messages.site.currentLevel}</h2>
		<div class="levelcol">
			<p class="label">{messages.site.currentLevel}</p>
			<LevelGauge {level} thresholds={site.thresholds} stale={fresh.stale} />
			<p class="muted ref">{messages.site.referenceNote}</p>
			{#if level !== null}<p class="sr-only">{formatLevel(level)}</p>{/if}
		</div>
		<dl class="facts">
			<div>
				<dt>{messages.site.activeSeverity}</dt>
				<dd><SeverityBadge severity={sev} /></dd>
			</div>
			<div>
				<dt>{messages.site.riseRate}</dt>
				<dd>
					{#if rate === null || trend === null}
						<span class="muted">{messages.site.riseRateNeedMore}</span>
					{:else}
						<span class="rate {trend}">
							<Icon
								name={trend === 'rising' ? 'up' : trend === 'falling' ? 'down' : 'flat'}
								size={20}
							/>
							<strong>{rate > 0 ? '+' : ''}{formatNumber(rate)} {messages.units.cmPerHour}</strong>
							<span
								>{trend === 'rising'
									? messages.site.riseRateRising
									: trend === 'falling'
										? messages.site.riseRateFalling
										: messages.site.riseRateSteady}</span
							>
						</span>
						<small class="muted">{messages.site.riseRateWindow}</small>
					{/if}
				</dd>
			</div>
			<div>
				<dt>{messages.site.lastUpdated}</dt>
				<dd>
					{#if updatedAt}
						<strong>{formatDateTime(updatedAt, site.tz)}</strong>
						<small class="muted">{formatAge(updatedAt, now)}</small>
					{:else}
						<span class="muted">{messages.stale.never}</span>
					{/if}
				</dd>
			</div>
			<div>
				<dt>{messages.site.deviceStatus}</dt>
				<dd><StatusBadge status={site.status} /></dd>
			</div>
		</dl>
	</section>

	<section class="card reveal" style="--i: 2" aria-labelledby="chart-title">
		<div class="row between">
			<h2 id="chart-title">{messages.site.chartTitle}</h2>
			<fieldset class="seg">
				<legend class="sr-only">{messages.site.rangeLabel}</legend>
				{#each RANGE_IDS as id (id)}
					<label>
						<input type="radio" name="range" value={id} bind:group={range} />
						<span>{messages.site.ranges[id]}</span>
					</label>
				{/each}
			</fieldset>
		</div>

		{#if seriesState === 'error'}
			<div class="notice bad" role="alert">
				<Icon name="alert" size={20} />
				<p>
					{messages.site.chartError}
					<button class="btn secondary small" type="button" onclick={() => (range = range)}
						>{messages.retry}</button
					>
				</p>
			</div>
		{:else if seriesState === 'ok' && series && series.points.length === 0}
			<p class="muted center">{messages.site.chartEmpty}</p>
		{:else}
			<Chart
				points={series?.points ?? []}
				res={series?.res ?? 'raw'}
				tz={site.tz}
				thresholds={site.thresholds}
				alerts={site.alerts}
				fromMs={series?.fromMs ?? now - 86_400_000}
				nowMs={series?.toMs ?? now}
				label="{messages.site.chartTitle}: {site.name}"
			/>
		{/if}

		{#if series && seriesState === 'ok'}
			<p class="muted small">
				{messages.site.resolutionNote(messages.site.resolution[series.res])}
			</p>
			{#if series.truncated}<p class="notice warn small">{messages.site.chartTruncated}</p>{/if}
			{#if tableRows.length}
				<details class="tbl">
					<summary>{messages.site.tableToggle}</summary>
					<div class="table-wrap">
						<table>
							<thead>
								<tr>
									<th>{messages.site.tableTime}</th>
									<th
										>{series.res === 'raw'
											? messages.site.tableValue
											: messages.site.tableRange}</th
									>
								</tr>
							</thead>
							<tbody>
								{#each tableRows as p (p.t)}
									<tr>
										<td>{formatDateTime(new Date(p.t), site.tz)}</td>
										<td>
											{#if p.min !== undefined && p.max !== undefined}
												{formatNumber(p.min)} - {formatNumber(p.max)} (rata-rata {formatNumber(
													p.v
												)})
											{:else}
												{formatNumber(p.v)}
											{/if}
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				</details>
			{/if}
		{/if}
	</section>

	<div class="grid cols-2 reveal" style="--i: 3">
		<section class="card" aria-labelledby="alerts-title">
			<h2 id="alerts-title">{messages.site.alertsTitle}</h2>
			{#if site.alerts.length === 0}
				<p class="muted">{messages.site.alertsEmpty}</p>
			{:else}
				<ul class="alerts">
					{#each site.alerts as a (a.opened_at + a.severity)}
						<li>
							<SeverityBadge severity={a.severity} quiet />
							<span class="badge" class:warn={a.state === 'OPEN'} class:idle={a.state !== 'OPEN'}>
								{a.state === 'OPEN' ? messages.site.alertOpen : messages.site.alertClosed}
							</span>
							<span class="muted">
								{messages.site.alertSpan(
									formatDateTime(a.opened_at, site.tz),
									a.closed_at
										? formatDateTime(a.closed_at, site.tz)
										: messages.site.alertOpen.toLowerCase()
								)}
								{#if a.peak_level_cm !== null}· {messages.site.alertPeak(
										formatLevel(a.peak_level_cm)
									)}{/if}
							</span>
						</li>
					{/each}
				</ul>
			{/if}
		</section>

		<section class="card" aria-labelledby="tg-title">
			<h2 id="tg-title">{messages.site.telegramTitle}</h2>
			{#if site.telegram_url}
				<a class="btn" href={site.telegram_url} rel="noopener noreferrer" target="_blank">
					<Icon name="send" size={18} />{messages.site.telegramButton}
				</a>
			{:else}
				<p class="muted">{messages.site.telegramNone}</p>
			{/if}
			{#if site.devices.length > 1}
				<h3 class="devh">{messages.site.devices}</h3>
				<ul class="devs">
					{#each site.devices as d (d.id)}
						<li>
							<span>{d.name || messages.site.unnamed}</span><span>{formatLevel(d.level_cm)}</span
							><StatusBadge status={d.status} />
						</li>
					{/each}
				</ul>
			{/if}
		</section>
	</div>
</div>

<style>
	.page {
		padding-block: 1rem 1rem;
		display: grid;
		gap: 1rem;
	}
	.crumb {
		margin: 0;
	}
	.head h1 {
		margin: 0;
	}
	.head p {
		margin: 0.3rem 0 0;
		display: flex;
		gap: 0.6rem 1rem;
		flex-wrap: wrap;
		align-items: center;
	}
	.livepill {
		display: inline-flex;
		align-items: center;
		gap: 0.4rem;
		padding: 0.1rem 0.65rem;
		border-radius: 999px;
		border: 1.5px solid var(--idle);
		color: var(--idle);
		font-weight: 650;
		font-size: 0.85rem;
	}
	.livepill.on {
		color: var(--ok);
		border-color: var(--ok);
	}
	.dot {
		width: 0.55rem;
		height: 0.55rem;
		border-radius: 50%;
		background: currentColor;
	}
	.on .dot {
		animation: pulse-ring 1.8s ease-out infinite;
	}
	.card + .card {
		margin-top: 0;
	}
	.hero {
		display: grid;
		gap: 1.5rem 2.5rem;
		grid-template-columns: minmax(0, 1fr);
		background:
			radial-gradient(
				60rem 20rem at 0% 0%,
				color-mix(in srgb, var(--brand) 10%, transparent),
				transparent 60%
			),
			var(--surface);
	}
	@media (min-width: 52rem) {
		.hero {
			grid-template-columns: auto minmax(0, 1fr);
			align-items: center;
		}
	}
	.label {
		margin: 0 0 0.35rem;
		font-weight: 700;
		color: var(--ink-2);
		text-transform: uppercase;
		letter-spacing: 0.05em;
		font-size: 0.82rem;
	}
	.ref {
		margin: 0.4rem 0 0;
		font-size: 0.9rem;
	}
	.facts {
		margin: 0;
		display: grid;
		gap: 1rem;
		grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
	}
	.facts dt {
		font-size: 0.82rem;
		font-weight: 700;
		color: var(--ink-2);
		text-transform: uppercase;
		letter-spacing: 0.04em;
		margin-bottom: 0.3rem;
	}
	.facts dd {
		margin: 0;
		display: grid;
		gap: 0.15rem;
		justify-items: start;
	}
	.rate {
		display: inline-flex;
		align-items: center;
		gap: 0.4rem;
		font-size: 1.1rem;
	}
	.rate.rising {
		color: var(--bad);
	}
	.rate.falling {
		color: var(--ok);
	}
	.rate.steady {
		color: var(--idle);
	}
	.seg {
		display: inline-flex;
		border: 1.5px solid var(--line);
		border-radius: 999px;
		padding: 0.2rem;
		gap: 0.15rem;
		background: var(--surface-2);
	}
	.seg label {
		position: relative;
	}
	.seg input {
		position: absolute;
		opacity: 0;
		inset: 0;
		width: 100%;
		height: 100%;
		margin: 0;
		cursor: pointer;
	}
	.seg span {
		display: inline-flex;
		align-items: center;
		min-height: 2.5rem;
		padding: 0 0.9rem;
		border-radius: 999px;
		font-weight: 650;
		transition:
			background-color 0.2s,
			color 0.2s;
	}
	.seg input:checked + span {
		background: var(--brand);
		color: var(--on-brand);
	}
	.seg input:focus-visible + span {
		outline: 3px solid var(--focus);
		outline-offset: 2px;
	}
	.center {
		text-align: center;
		padding: 2rem 0;
	}
	.small {
		font-size: 0.88rem;
		margin: 0.6rem 0 0;
	}
	.tbl {
		margin-top: 0.75rem;
	}
	.tbl summary {
		cursor: pointer;
		font-weight: 650;
		min-height: 2.75rem;
		display: flex;
		align-items: center;
	}
	.alerts,
	.devs {
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		gap: 0.6rem;
	}
	.alerts li {
		display: flex;
		flex-wrap: wrap;
		gap: 0.4rem 0.6rem;
		align-items: center;
	}
	.devs li {
		display: grid;
		grid-template-columns: 1fr auto auto;
		gap: 0.6rem;
		align-items: center;
	}
	.devh {
		margin-top: 1.25rem;
	}
</style>
