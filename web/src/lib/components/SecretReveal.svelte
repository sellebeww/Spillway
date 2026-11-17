<script lang="ts">
	import { beforeNavigate } from '$app/navigation';
	import { messages } from '#lib/messages.ts';
	import CopyButton from './CopyButton.svelte';
	import Icon from './Icon.svelte';

	// A device secret (or invitation link) that the API will never show again.
	let {
		title,
		subtitle,
		value,
		warning,
		ondismiss
	}: { title: string; subtitle?: string; value: string; warning: string; ondismiss: () => void } =
		$props();

	let saved = $state(false);
	let nudge = $state(false);

	// Leaving the page loses the value for good, so ask first.
	beforeNavigate((nav) => {
		if (!saved && !nav.willUnload && !confirm(messages.devices.secretNeedConfirm)) nav.cancel();
	});

	function dismiss() {
		if (!saved) {
			nudge = true;
			return;
		}
		ondismiss();
	}
</script>

<section class="secret reveal" role="alert" aria-labelledby="secret-title">
	<div class="head">
		<Icon name="lock" size={26} />
		<div>
			<h2 id="secret-title">{title}</h2>
			{#if subtitle}<p class="muted">{subtitle}</p>{/if}
		</div>
	</div>
	<p class="warn"><Icon name="alert" size={18} /> <strong>{warning}</strong></p>
	<div class="value">
		<code class="mono" data-testid="secret-value">{value}</code>
		<CopyButton text={value} />
	</div>
	<div class="check">
		<input id="secret-saved" type="checkbox" bind:checked={saved} />
		<label for="secret-saved">{messages.devices.secretSaved}</label>
	</div>
	{#if nudge && !saved}<p class="err" role="alert">{messages.devices.secretNeedConfirm}</p>{/if}
	<button type="button" class="btn secondary" onclick={dismiss} aria-disabled={!saved}
		>{messages.devices.secretDismiss}</button
	>
</section>

<style>
	.secret {
		border: 3px solid var(--warn);
		border-radius: var(--radius);
		background:
			repeating-linear-gradient(
				135deg,
				transparent 0 14px,
				color-mix(in srgb, var(--warn) 10%, transparent) 14px 28px
			),
			var(--surface);
		padding: 1.25rem;
		display: grid;
		gap: 0.9rem;
		margin-bottom: 1.25rem;
	}
	.head {
		display: flex;
		gap: 0.75rem;
		align-items: center;
		color: var(--warn);
	}
	h2,
	.head p {
		margin: 0;
	}
	.warn {
		margin: 0;
		color: var(--warn);
		display: flex;
		gap: 0.5rem;
		align-items: flex-start;
	}
	.value {
		display: flex;
		flex-wrap: wrap;
		gap: 0.75rem;
		align-items: center;
		padding: 0.75rem 1rem;
		background: var(--surface-2);
		border: 1.5px solid var(--ink-2);
		border-radius: var(--radius-sm);
	}
	code {
		word-break: break-all;
		flex: 1 1 16rem;
		font-size: 1.02rem;
		user-select: all;
	}
	.err {
		color: var(--bad);
		font-weight: 600;
		margin: 0;
	}
</style>
