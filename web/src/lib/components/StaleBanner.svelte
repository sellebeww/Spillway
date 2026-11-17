<script lang="ts">
	import { messages } from '#lib/messages.ts';
	import Icon from './Icon.svelte';

	// Fail-visible: stale data must never look current.
	let {
		updatedText,
		connectionLost = false
	}: { updatedText: string | null; connectionLost?: boolean } = $props();
</script>

<div class="stale" role="alert">
	<span class="ico"><Icon name="clock" size={34} /></span>
	<div>
		<p class="big">{messages.stale.label}</p>
		<p class="when">
			{updatedText ? messages.stale.lastUpdate(updatedText) : messages.stale.never}
		</p>
		<p class="why">{connectionLost ? messages.stale.connectionLost : messages.stale.warning}</p>
	</div>
</div>

<style>
	.stale {
		display: flex;
		gap: 1rem;
		align-items: center;
		padding: 1rem 1.25rem;
		border-radius: var(--radius);
		border: 3px solid var(--warn);
		color: var(--warn);
		background:
			repeating-linear-gradient(
				135deg,
				transparent 0 14px,
				color-mix(in srgb, var(--warn) 14%, transparent) 14px 28px
			),
			var(--warn-bg);
		animation: rise 0.4s var(--ease) both;
	}
	.ico {
		display: grid;
		place-items: center;
		flex: none;
		width: 3.4rem;
		height: 3.4rem;
		border-radius: 50%;
		border: 3px solid currentColor;
		animation: pulse-ring 2s ease-out infinite;
	}
	.big {
		margin: 0;
		font-size: clamp(1.4rem, 5vw, 2rem);
		font-weight: 850;
		letter-spacing: 0.02em;
		line-height: 1.1;
	}
	.when {
		margin: 0.25rem 0 0;
		font-weight: 700;
		font-size: 1.05rem;
	}
	.why {
		margin: 0.2rem 0 0;
		font-size: 0.92rem;
		color: var(--ink);
		font-weight: 500;
	}
</style>
