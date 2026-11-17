<script lang="ts">
	import Empty from '#lib/components/Empty.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import { messages } from '#lib/messages.ts';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();
</script>

<svelte:head
	><title>{messages.sites.title} · {messages.admin.title} · {messages.appName}</title></svelte:head
>

<div class="row between head">
	<div>
		<h1>{messages.sites.title}</h1>
		<p class="muted">{messages.sites.intro}</p>
	</div>
	{#if data.me?.admin}
		<a class="btn" href="/admin/sites/baru"><Icon name="plus" size={18} />{messages.sites.new}</a>
	{/if}
</div>

{#if data.sites.length === 0}
	<Empty
		title={messages.admin.noSites}
		body={data.me?.admin ? messages.admin.noSitesHint : messages.admin.noSitesOperator}
	>
		{#if data.me?.admin}<a class="btn" href="/admin/sites/baru">{messages.admin.createFirstSite}</a
			>{/if}
	</Empty>
{:else}
	<div class="table-wrap reveal">
		<table>
			<caption class="sr-only">{messages.sites.title}</caption>
			<thead>
				<tr>
					<th scope="col">{messages.sites.colName}</th>
					<th scope="col">{messages.sites.colSlug}</th>
					<th scope="col">{messages.sites.colZone}</th>
					<th scope="col">{messages.sites.colPublic}</th>
					<th scope="col">{messages.sites.colActions}</th>
				</tr>
			</thead>
			<tbody>
				{#each data.sites as s (s.id)}
					<tr>
						<th scope="row" class="name">{s.name}</th>
						<td class="mono">{s.slug}</td>
						<td>{s.tz}</td>
						<td>
							{#if s.is_public}
								<span class="badge ok"><Icon name="check" size={14} />{messages.sites.public}</span>
							{:else}
								<span class="badge idle"
									><Icon name="lock" size={14} />{messages.sites.private}</span
								>
							{/if}
						</td>
						<td>
							<span class="row">
								{#if data.me?.admin}<a class="btn secondary small" href="/admin/sites/{s.id}"
										>{messages.edit}</a
									>{/if}
								<a class="btn secondary small" href="/admin/devices?site={s.id}"
									>{messages.nav.devices}</a
								>
								{#if s.is_public}<a class="btn secondary small" href="/s/{s.slug}"
										>{messages.sites.viewPublic}</a
									>{/if}
							</span>
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
{/if}

<style>
	.head {
		margin-bottom: 1rem;
		align-items: end;
	}
	h1 {
		margin: 0;
	}
	.head p {
		margin: 0.25rem 0 0;
	}
	.name {
		text-transform: none;
		letter-spacing: 0;
		font-size: 1rem;
		color: var(--ink);
		background: none;
		white-space: normal;
	}
</style>
