<script lang="ts">
	import { messages } from '#lib/messages.ts';

	// Plain links, so paging works without JavaScript and can be bookmarked.
	let {
		offset,
		limit,
		total,
		href
	}: { offset: number; limit: number; total: number; href: (offset: number) => string } = $props();

	const from = $derived(total === 0 ? 0 : offset + 1);
	const to = $derived(Math.min(offset + limit, total));
</script>

{#if total > limit}
	<nav class="pager row between" aria-label={messages.pageOf(from, to, total)}>
		<span class="muted">{messages.pageOf(from, to, total)}</span>
		<span class="row">
			{#if offset > 0}
				<a class="btn secondary small" href={href(Math.max(0, offset - limit))} rel="prev"
					>{messages.previous}</a
				>
			{/if}
			{#if offset + limit < total}
				<a class="btn secondary small" href={href(offset + limit)} rel="next">{messages.next}</a>
			{/if}
		</span>
	</nav>
{/if}

<style>
	.pager {
		margin-top: 1rem;
	}
</style>
