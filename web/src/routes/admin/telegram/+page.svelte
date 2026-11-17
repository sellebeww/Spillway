<script lang="ts">
	import { enhance } from '$app/forms';
	import Empty from '#lib/components/Empty.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import SecretReveal from '#lib/components/SecretReveal.svelte';
	import SiteSelect from '#lib/components/SiteSelect.svelte';
	import { formatDateTime } from '#lib/format.ts';
	import { messages } from '#lib/messages.ts';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	let shown = $state<{ url: string; expiresAt: string } | null>(null);
	$effect(() => {
		if (form && 'link' in form && form.link) shown = form.link;
	});
	const formError = $derived(form && 'formError' in form ? (form.formError as string) : null);
</script>

<svelte:head
	><title>{messages.telegram.title} · {messages.admin.title} · {messages.appName}</title
	></svelte:head
>

<h1>{messages.telegram.title}</h1>
<p class="muted">{messages.telegram.intro}</p>

{#if !data.site}
	<Empty title={messages.admin.noSites} body={messages.admin.noSitesHint} />
{:else}
	<SiteSelect sites={data.sites} selected={data.site.id} />

	{#if shown}
		<SecretReveal
			title={messages.telegram.linkTitle}
			subtitle={messages.telegram.linkExpires(formatDateTime(shown.expiresAt, data.site.tz))}
			value={shown.url}
			warning={messages.telegram.linkWarn}
			ondismiss={() => (shown = null)}
		/>
	{/if}
	{#if formError}<p class="notice bad" role="alert">
			<Icon name="alert" size={20} />
			{formError}
		</p>{/if}
	{#if form && 'revoked' in form && form.revoked !== undefined}
		<p class="notice ok" role="status">
			<Icon name="check" size={20} />
			{messages.telegram.revoked(form.revoked as number)}
		</p>
	{/if}

	<div class="row actions">
		<form
			method="POST"
			action="?/link"
			use:enhance={() =>
				async ({ update }) =>
					update({ reset: false })}
		>
			<input type="hidden" name="site" value={data.site.id} />
			<button class="btn" type="submit"
				><Icon name="send" size={18} />{messages.telegram.generate}</button
			>
		</form>
		<form
			method="POST"
			action="?/revoke"
			use:enhance={({ cancel }) => {
				if (!confirm(messages.telegram.revokeConfirm)) cancel();
				return async ({ update }) => update({ reset: false });
			}}
		>
			<input type="hidden" name="site" value={data.site.id} />
			<button class="btn secondary" type="submit">{messages.telegram.revoke}</button>
		</form>
	</div>

	<section class="card" aria-labelledby="ch-title">
		<h2 id="ch-title">{messages.telegram.channels}</h2>
		{#if data.loadError}
			<p class="notice bad" role="alert">{data.loadError}</p>
		{:else if data.channels.length === 0}
			<Empty
				title={messages.telegram.channelsEmpty}
				body={messages.telegram.channelsEmptyHint}
				icon="send"
			/>
		{:else}
			<div class="table-wrap">
				<table>
					<thead
						><tr
							><th scope="col">{messages.telegram.colChat}</th><th scope="col"
								>{messages.telegram.colSince}</th
							></tr
						></thead
					>
					<tbody>
						{#each data.channels as c (c.id)}
							<tr
								><td class="mono">{c.chat_hint}</td><td
									>{formatDateTime(c.created_at, data.site.tz)}</td
								></tr
							>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</section>
{/if}

<style>
	h1 {
		margin-bottom: 0.25rem;
	}
	.actions {
		margin: 1rem 0;
	}
</style>
