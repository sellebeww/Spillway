<script lang="ts">
	import { page } from '$app/state';
	import Empty from '#lib/components/Empty.svelte';
	import { messages } from '#lib/messages.ts';

	const notFound = $derived(page.status === 404);
	const auth = $derived(page.url.pathname.startsWith('/auth/'));
</script>

<svelte:head><title>{page.status} · {messages.appName}</title></svelte:head>

<div class="container wrap">
	<Empty
		icon={notFound ? 'pin' : 'alert'}
		tone={notFound ? 'neutral' : 'bad'}
		title={notFound ? messages.site.notFoundTitle : `${page.status}`}
		body={notFound ? messages.site.notFoundBody : page.error?.message}
	>
		<a class="btn" href="/">{messages.site.backToMap}</a>
		{#if auth}
			<a class="btn secondary" href="/auth/login?next=/admin/sites" data-sveltekit-reload
				>{messages.nav.signIn}</a
			>
		{:else if !notFound}
			<a class="btn secondary" href={page.url.pathname}>{messages.retry}</a>
		{/if}
	</Empty>
</div>

<style>
	.wrap {
		padding-block: 3rem;
	}
</style>
