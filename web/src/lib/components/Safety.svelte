<script lang="ts">
	import { messages } from '#lib/messages.ts';
	import Icon from './Icon.svelte';
</script>

<aside class="safety" aria-labelledby="safety-title">
	<Icon name="info" size={22} />
	<div>
		<h2 id="safety-title">{messages.safetyTitle}</h2>
		<p>{messages.safety}</p>
	</div>
</aside>

<style>
	.safety {
		display: flex;
		gap: 0.85rem;
		align-items: flex-start;
		padding: 1rem 1.15rem;
		border: 1.5px solid var(--warn);
		border-left-width: 6px;
		border-radius: var(--radius-sm);
		background: var(--warn-bg);
		color: var(--ink);
	}
	.safety :global(svg) {
		color: var(--warn);
		margin-top: 0.2rem;
	}
	h2 {
		font-size: 1rem;
		margin: 0 0 0.25rem;
		color: var(--warn);
	}
	p {
		margin: 0;
		font-size: 0.95rem;
	}
</style>
