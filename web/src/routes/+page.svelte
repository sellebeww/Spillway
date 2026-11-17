<script lang="ts">
	import { onMount } from 'svelte';
	import { invalidate } from '$app/navigation';
	import Empty from '#lib/components/Empty.svelte';
	import StatusBadge from '#lib/components/StatusBadge.svelte';
	import SiteMap from '#lib/components/SiteMap.svelte';
	import { formatAge, formatDateTime, formatLevel } from '#lib/format.ts';
	import { openLive, type LiveState } from '#lib/live.ts';
	import { messages } from '#lib/messages.ts';
	import { deriveFreshness, isFreshReading } from '#lib/stale.ts';
	import type { LiveReading, PublicSiteFeature } from '#lib/types.ts';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	// Server data is the truth; live readings patch it until the next refresh replaces it.
	let live = $state<Record<string, { level: number; ts: number }>>({});
	let lastOk = $state(Date.now());
	let liveAt = $state<number | null>(null);
	let now = $state(Date.now());
	let liveState = $state<LiveState>('connecting');
	let mapFailed = $state(false);

	$effect(() => {
		void data.sites;
		live = {};
		liveAt = null;
		if (data.sites) lastOk = Date.now();
	});

	const fresh = $derived(
		deriveFreshness({ apiStale: false, lastOkMs: lastOk, nowMs: now, liveReadingAtMs: liveAt })
	);

	const sites = $derived<PublicSiteFeature[]>(
		(data.sites ?? []).map((f) => {
			const l = live[f.properties.slug];
			const p = { ...f.properties };
			if (l && (!p.updated_at || l.ts >= Date.parse(p.updated_at))) {
				p.level_cm = l.level;
				p.updated_at = new Date(l.ts).toISOString();
				p.status = 'ONLINE';
				p.stale = false;
				p.stale_label = null;
			}
			// Fail-visible: with the server unreachable nothing here can be vouched for.
			if (fresh.connectionLost) {
				p.stale = true;
				p.stale_label = messages.stale.label;
			}
			return { ...f, properties: p };
		})
	);

	function onReading(r: LiveReading) {
		const ts = r.ts * 1000;
		// Fail-visible: only a reading that is recent by its own timestamp may clear a stale label.
		if (r.late || !isFreshReading(ts, Date.now())) return;
		if (live[r.site] && live[r.site].ts > ts) return;
		live = { ...live, [r.site]: { level: r.level_cm, ts } };
		liveAt = Date.now();
	}

	onMount(() => {
		const handle = openLive(data.apiBase, null, { onReading, onState: (s) => (liveState = s) });
		const tick = setInterval(() => (now = Date.now()), 15_000);
		const poll = setInterval(() => {
			if (!document.hidden) invalidate('app:map');
		}, 30_000);
		const onVisible = () => {
			if (!document.hidden) invalidate('app:map');
		};
		document.addEventListener('visibilitychange', onVisible);
		return () => {
			handle.close();
			clearInterval(tick);
			clearInterval(poll);
			document.removeEventListener('visibilitychange', onVisible);
		};
	});
</script>

<svelte:head>
	<title>{messages.map.title} · {messages.appName}</title>
	<meta name="description" content={messages.map.description} />
</svelte:head>

<div class="container page">
	<div class="head reveal">
		<div>
			<h1>{messages.map.title}</h1>
			<p class="muted">{messages.tagline}</p>
		</div>
		<p class="livepill" class:on={liveState === 'open'} role="status">
			<span class="dot" aria-hidden="true"></span>
			{liveState === 'open'
				? messages.map.liveOn
				: liveState === 'connecting'
					? messages.map.liveConnecting
					: messages.map.liveOff}
		</p>
	</div>

	{#if data.sites === null}
		<Empty
			icon="signal"
			tone="bad"
			title={messages.map.loadError}
			body={messages.map.loadErrorHint}
		>
			<a class="btn" href="/" data-sveltekit-reload>{messages.retry}</a>
		</Empty>
	{:else if sites.length === 0}
		<Empty title={messages.map.empty} body={messages.map.emptyHint} />
	{:else}
		{#if fresh.connectionLost}
			<div class="notice warn" role="alert" style="margin-bottom: 1rem">
				<p><strong>{messages.stale.label}.</strong> {messages.stale.connectionLost}</p>
			</div>
		{/if}

		<div class="layout">
			<div class="reveal" style="--i: 1">
				{#if mapFailed}
					<p class="notice warn">{messages.map.unavailable}</p>
				{:else}
					<SiteMap
						{sites}
						tileUrl={data.tileUrl}
						attribution={data.attribution}
						onfail={() => (mapFailed = true)}
					/>
				{/if}
				<p class="muted tiny">{messages.map.tileNote}</p>
			</div>

			<section aria-labelledby="list-title" class="listcol">
				<h2 id="list-title">{messages.map.listTitle}</h2>
				<ul class="sites">
					{#each sites as s, i (s.properties.slug)}
						{@const p = s.properties}
						<li class="reveal" style="--i: {i + 2}">
							<a class="site" class:stale={p.stale} href="/s/{encodeURIComponent(p.slug)}">
								<span class="name">{p.name}</span>
								<span class="lvl">{formatLevel(p.level_cm)}</span>
								<span class="meta">
									<StatusBadge status={p.status} />
									{#if p.stale}<span class="badge warn">{messages.stale.label}</span>{/if}
								</span>
								<span class="when muted">
									{#if p.updated_at}
										{messages.map.updated}: {formatDateTime(p.updated_at, p.tz)} ({formatAge(
											p.updated_at,
											now
										)})
									{:else}
										{messages.stale.never}
									{/if}
								</span>
							</a>
						</li>
					{/each}
				</ul>
			</section>
		</div>
	{/if}
</div>

<style>
	.page {
		padding-block: 1.5rem;
	}
	.head {
		display: flex;
		flex-wrap: wrap;
		justify-content: space-between;
		align-items: end;
		gap: 0.75rem 1.5rem;
		margin-bottom: 1.25rem;
	}
	h1 {
		margin: 0;
	}
	.head p {
		margin: 0.25rem 0 0;
	}
	.livepill {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
		padding: 0.35rem 0.85rem;
		border-radius: 999px;
		border: 1.5px solid var(--idle);
		color: var(--idle);
		background: var(--idle-bg);
		font-weight: 650;
		font-size: 0.9rem;
	}
	.livepill.on {
		color: var(--ok);
		border-color: var(--ok);
		background: var(--ok-bg);
	}
	.dot {
		width: 0.6rem;
		height: 0.6rem;
		border-radius: 50%;
		background: currentColor;
	}
	.on .dot {
		animation: pulse-ring 1.8s ease-out infinite;
	}
	.layout {
		display: grid;
		gap: 1.5rem;
		grid-template-columns: minmax(0, 1fr);
	}
	@media (min-width: 62rem) {
		.layout {
			grid-template-columns: minmax(0, 2fr) minmax(18rem, 1fr);
			align-items: start;
		}
		.listcol {
			max-height: 40rem;
			overflow-y: auto;
			padding-right: 0.25rem;
		}
	}
	.tiny {
		font-size: 0.82rem;
		margin: 0.5rem 0 0;
	}
	.sites {
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		gap: 0.75rem;
	}
	.site {
		display: grid;
		gap: 0.3rem 0.75rem;
		grid-template-columns: 1fr auto;
		padding: 0.9rem 1rem;
		background: var(--surface);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		box-shadow: var(--shadow);
		color: var(--ink);
		text-decoration: none;
		transition:
			transform 0.2s var(--ease),
			box-shadow 0.2s var(--ease),
			border-color 0.2s;
	}
	.site:hover {
		transform: translateY(-2px);
		box-shadow: var(--shadow-lg);
		border-color: var(--brand);
		color: var(--ink);
	}
	.site.stale {
		border-color: var(--warn);
		border-style: dashed;
		border-width: 2px;
	}
	.name {
		font-weight: 750;
		font-size: 1.05rem;
	}
	.lvl {
		font-weight: 850;
		font-size: 1.35rem;
		font-variant-numeric: tabular-nums;
		text-align: right;
	}
	.meta {
		display: flex;
		gap: 0.4rem;
		flex-wrap: wrap;
	}
	.when {
		grid-column: 1 / -1;
		font-size: 0.88rem;
	}
</style>
