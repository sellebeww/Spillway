<script lang="ts">
	import type { Snippet } from 'svelte';
	import Icon from './Icon.svelte';

	// Empty and error states share one look: icon, a clear sentence, what to do next.
	let {
		title,
		body,
		icon = 'water',
		tone = 'neutral',
		children
	}: {
		title: string;
		body?: string;
		icon?: string;
		tone?: 'neutral' | 'bad';
		children?: Snippet;
	} = $props();
</script>

<div class="empty reveal" class:bad={tone === 'bad'} role={tone === 'bad' ? 'alert' : undefined}>
	<span class="ico"><Icon name={icon} size={30} /></span>
	<h2>{title}</h2>
	{#if body}<p>{body}</p>{/if}
	{#if children}<div class="actions">{@render children()}</div>{/if}
</div>

<style>
	.empty {
		display: grid;
		justify-items: center;
		text-align: center;
		gap: 0.35rem;
		padding: 2.25rem 1rem;
		border: 2px dashed var(--line);
		border-radius: var(--radius);
		background: color-mix(in srgb, var(--surface) 70%, transparent);
	}
	.empty.bad {
		border-color: var(--bad);
	}
	.ico {
		display: grid;
		place-items: center;
		width: 4rem;
		height: 4rem;
		border-radius: 50%;
		background: var(--surface-2);
		color: var(--brand);
		animation: bob 3.2s ease-in-out infinite;
	}
	.bad .ico {
		color: var(--bad);
		background: var(--bad-bg);
	}
	h2 {
		font-size: 1.15rem;
		margin: 0.4rem 0 0;
	}
	p {
		margin: 0;
		color: var(--ink-2);
		max-width: 36rem;
	}
	.actions {
		margin-top: 0.75rem;
		display: flex;
		gap: 0.6rem;
		flex-wrap: wrap;
		justify-content: center;
	}
</style>
