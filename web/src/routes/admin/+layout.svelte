<script lang="ts">
	import { page } from '$app/state';
	import Empty from '#lib/components/Empty.svelte';
	import { messages } from '#lib/messages.ts';
	import type { LayoutProps } from './$types';

	let { data, children }: LayoutProps = $props();

	const links = $derived(
		[
			{ href: '/admin/sites', label: messages.nav.sites },
			{ href: '/admin/devices', label: messages.nav.devices },
			{ href: '/admin/calibration', label: messages.nav.calibration },
			{ href: '/admin/rules', label: messages.nav.rules },
			{ href: '/admin/telegram', label: messages.nav.telegram },
			{ href: '/admin/alerts', label: messages.nav.alerts },
			...(data.me?.admin ? [{ href: '/admin/audit', label: messages.nav.audit }] : [])
		].map((l) => ({ ...l, current: page.url.pathname.startsWith(l.href) }))
	);
	const role = $derived(data.me?.admin ? 'admin' : 'operator');
</script>

<svelte:head><meta name="robots" content="noindex" /></svelte:head>

<div class="container shell">
	{#if data.fatal || !data.me}
		<Empty
			icon="lock"
			tone="bad"
			title={messages.admin.title}
			body={data.fatal ?? messages.admin.accountNotLinked}
		>
			<a class="btn" href="/admin/sites" data-sveltekit-reload>{messages.retry}</a>
		</Empty>
	{:else}
		<div class="who reveal">
			<span class="badge ok">{messages.admin.role[role]}</span>
			<span class="muted">{data.me.email ?? ''} · {messages.admin.roleHint[role]}</span>
		</div>
		<nav class="tabs" aria-label={messages.admin.navLabel}>
			{#each links as l (l.href)}
				<a href={l.href} aria-current={l.current ? 'page' : undefined}>{l.label}</a>
			{/each}
		</nav>
		{#if data.sitesError}
			<p class="notice bad" role="alert">{data.sitesError}</p>
		{/if}
		<div class="content">
			{@render children()}
		</div>
	{/if}
</div>

<style>
	.shell {
		padding-block: 1.25rem;
	}
	.who {
		display: flex;
		gap: 0.6rem 1rem;
		flex-wrap: wrap;
		align-items: center;
		margin-bottom: 0.75rem;
	}
	.tabs {
		display: flex;
		gap: 0.25rem;
		overflow-x: auto;
		padding: 0.25rem;
		border-radius: 999px;
		background: var(--surface-2);
		border: 1px solid var(--line);
		margin-bottom: 1.25rem;
		scrollbar-width: thin;
	}
	.tabs a {
		display: inline-flex;
		align-items: center;
		flex: none;
		min-height: 2.75rem;
		padding: 0 1rem;
		border-radius: 999px;
		font-weight: 650;
		color: var(--ink);
		text-decoration: none;
		transition:
			background-color 0.2s,
			color 0.2s;
	}
	.tabs a:hover {
		background: var(--surface);
		color: var(--ink);
	}
	.tabs a[aria-current='page'] {
		background: var(--brand);
		color: var(--on-brand);
	}
	.content {
		animation: rise 0.45s var(--ease) both;
	}
</style>
