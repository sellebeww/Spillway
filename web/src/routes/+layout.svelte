<script lang="ts">
	import { onNavigate } from '$app/navigation';
	import { page } from '$app/state';
	import favicon from '#lib/assets/favicon.svg';
	import Icon from '#lib/components/Icon.svelte';
	import Safety from '#lib/components/Safety.svelte';
	import { messages } from '#lib/messages.ts';
	import '#lib/styles.css';
	import type { LayoutProps } from './$types';

	let { data, children }: LayoutProps = $props();

	const inAdmin = $derived(
		page.url.pathname === '/admin' || page.url.pathname.startsWith('/admin/')
	);

	// Cross-fade between pages where the browser supports it; skipped for reduced motion.
	onNavigate((nav) => {
		if (!document.startViewTransition || matchMedia('(prefers-reduced-motion: reduce)').matches)
			return;
		return new Promise((resolve) => {
			document.startViewTransition(async () => {
				resolve();
				await nav.complete;
			});
		});
	});
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
	<meta name="description" content={messages.tagline} />
</svelte:head>

<a class="skip" href="#main">{messages.skipToContent}</a>

<header class="top no-print">
	<div class="container bar">
		<a class="brand" href="/" aria-label="{messages.appName}: {messages.nav.map}">
			<svg viewBox="0 0 40 40" width="34" height="34" aria-hidden="true" focusable="false">
				<defs><clipPath id="logo-clip"><circle cx="20" cy="20" r="18" /></clipPath></defs>
				<circle cx="20" cy="20" r="18" fill="var(--brand)" />
				<g clip-path="url(#logo-clip)">
					<g class="logo-wave"
						><path
							d="M0 22 Q 5 17 10 22 T 20 22 T 30 22 T 40 22 T 50 22 T 60 22 T 70 22 T 80 22 V 40 H 0Z"
							fill="#fff"
							opacity="0.92"
						/></g
					>
				</g>
			</svg>
			<span>{messages.appName}</span>
		</a>
		<nav aria-label={messages.nav.main} class="links">
			<a href="/" aria-current={page.url.pathname === '/' ? 'page' : undefined}>
				<Icon name="pin" size={18} />{messages.nav.map}
			</a>
			{#if data.signedIn}
				<a href="/admin/sites" aria-current={inAdmin ? 'page' : undefined}>
					<Icon name="list" size={18} />{messages.nav.admin}
				</a>
				<form method="POST" action="/auth/logout">
					<button
						class="linkbtn"
						type="submit"
						title={data.who ? messages.nav.signedInAs(data.who) : undefined}
					>
						<Icon name="logout" size={18} />{messages.nav.signOut}
					</button>
				</form>
			{:else}
				<a href="/auth/login?next=/admin/sites" data-sveltekit-reload>
					<Icon name="lock" size={18} />{messages.nav.signIn}
				</a>
			{/if}
		</nav>
	</div>
</header>

<main id="main" tabindex="-1">
	{@render children()}
</main>

<footer class="foot no-print">
	<div class="container">
		{#if inAdmin}
			<p class="muted small">{messages.safety}</p>
		{:else}
			<Safety />
		{/if}
		<p class="muted small">{messages.appName} · {messages.tagline}</p>
	</div>
</footer>

<style>
	.skip {
		position: absolute;
		left: 0.75rem;
		top: -4rem;
		z-index: 100;
		padding: 0.7rem 1rem;
		background: var(--brand);
		color: var(--on-brand);
		border-radius: var(--radius-sm);
		font-weight: 700;
	}
	.skip:focus {
		top: 0.75rem;
	}
	.top {
		position: sticky;
		top: 0;
		z-index: 20;
		backdrop-filter: saturate(1.4) blur(12px);
		background: color-mix(in srgb, var(--surface) 82%, transparent);
		border-bottom: 1px solid var(--line);
	}
	.bar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem 1rem;
		flex-wrap: wrap;
		min-height: 3.6rem;
		padding-block: 0.35rem;
	}
	.brand {
		display: inline-flex;
		align-items: center;
		gap: 0.6rem;
		font-weight: 850;
		font-size: 1.25rem;
		letter-spacing: -0.02em;
		color: var(--ink);
		text-decoration: none;
	}
	.logo-wave {
		width: 80px;
		animation: wave-slide 3.2s linear infinite;
	}
	.links {
		display: flex;
		align-items: center;
		gap: 0.25rem;
		flex-wrap: wrap;
	}
	.links a,
	.linkbtn {
		display: inline-flex;
		align-items: center;
		gap: 0.4rem;
		min-height: 2.75rem;
		padding: 0 0.85rem;
		border-radius: 999px;
		font: inherit;
		font-weight: 650;
		color: var(--ink);
		background: none;
		border: 0;
		text-decoration: none;
		cursor: pointer;
		transition: background-color 0.2s;
	}
	.links a:hover,
	.linkbtn:hover {
		background: var(--surface-2);
		color: var(--ink);
	}
	.links a[aria-current='page'] {
		background: var(--surface-2);
		box-shadow: inset 0 -3px 0 var(--brand);
		border-radius: 999px 999px 6px 6px;
	}
	main {
		flex: 1;
		outline: none;
	}
	.foot {
		margin-top: 3rem;
		padding-block: 1.5rem 2rem;
		border-top: 1px solid var(--line);
		background: color-mix(in srgb, var(--surface) 60%, transparent);
	}
	.small {
		font-size: 0.88rem;
		margin: 0.75rem 0 0;
	}
</style>
