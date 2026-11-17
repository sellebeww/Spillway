<script lang="ts">
	import Empty from '#lib/components/Empty.svelte';
	import Pager from '#lib/components/Pager.svelte';
	import { formatDateTime } from '#lib/format.ts';
	import { messages } from '#lib/messages.ts';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	// before/after are shown as text only (never parsed as HTML).
	const pretty = (v: unknown) => (v === null || v === undefined ? '-' : JSON.stringify(v, null, 2));
</script>

<svelte:head
	><title>{messages.audit.title} · {messages.admin.title} · {messages.appName}</title></svelte:head
>

<h1>{messages.audit.title}</h1>
<p class="muted">{messages.audit.intro}</p>

{#if data.forbidden}
	<Empty icon="lock" title={messages.admin.adminOnlyPage}
		><a class="btn" href="/admin/sites">{messages.back}</a></Empty
	>
{:else if data.loadError}
	<Empty icon="signal" tone="bad" title={data.loadError}
		><a class="btn" href="/admin/audit">{messages.retry}</a></Empty
	>
{:else if data.entries.length === 0}
	<Empty title={messages.audit.empty} />
{:else}
	<div class="table-wrap">
		<table>
			<thead>
				<tr>
					<th scope="col">{messages.audit.colTime}</th>
					<th scope="col">{messages.audit.colActor}</th>
					<th scope="col">{messages.audit.colAction}</th>
					<th scope="col">{messages.audit.colEntity}</th>
				</tr>
			</thead>
			<tbody>
				{#each data.entries as e (e.id)}
					<tr>
						<td>{formatDateTime(e.at, 'Asia/Jakarta')}</td>
						<td>{e.actor === 'system' ? messages.audit.system : e.actor}</td>
						<td><span class="badge idle">{e.action}</span></td>
						<td>
							<span class="mono">{e.entity_type}</span>
							<details>
								<summary>{messages.audit.showChange}</summary>
								<div class="diff">
									<div>
										<strong>{messages.audit.before}</strong>
										<pre>{pretty(e.before)}</pre>
									</div>
									<div>
										<strong>{messages.audit.after}</strong>
										<pre>{pretty(e.after)}</pre>
									</div>
								</div>
							</details>
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
	<Pager offset={data.offset} limit={data.limit} total={data.total} href={(o) => `?offset=${o}`} />
{/if}

<style>
	h1 {
		margin-bottom: 0.25rem;
	}
	summary {
		cursor: pointer;
		color: var(--brand);
		min-height: 2.75rem;
		display: flex;
		align-items: center;
	}
	.diff {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(14rem, 1fr));
		gap: 0.75rem;
	}
	pre {
		margin: 0.25rem 0 0;
		padding: 0.6rem;
		background: var(--surface-2);
		border-radius: var(--radius-sm);
		overflow-x: auto;
		font-size: 0.85rem;
		max-height: 16rem;
	}
</style>
