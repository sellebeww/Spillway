<script lang="ts">
	import type { Snippet } from 'svelte';
	import { messages } from '#lib/messages.ts';
	import Icon from './Icon.svelte';

	export interface ControlAttrs {
		id: string;
		'aria-describedby': string | undefined;
		'aria-invalid': true | undefined;
		'aria-required': true | undefined;
	}

	// Every control has a visible label. Hints and errors are tied to it with aria-describedby.
	let {
		id,
		label,
		hint,
		error,
		required = false,
		control
	}: {
		id: string;
		label: string;
		hint?: string;
		error?: string;
		required?: boolean;
		control: Snippet<[ControlAttrs]>;
	} = $props();

	const describedby = $derived(
		[hint ? `${id}-hint` : '', error ? `${id}-err` : ''].filter(Boolean).join(' ') || undefined
	);
</script>

<div class="field">
	<label for={id}>
		{label}
		{#if required}<span class="req" title={messages.required}>*</span>{/if}
	</label>
	{@render control({
		id,
		'aria-describedby': describedby,
		'aria-invalid': error ? true : undefined,
		'aria-required': required ? true : undefined
	})}
	{#if hint}<span class="hint" id="{id}-hint">{hint}</span>{/if}
	{#if error}
		<span class="err" id="{id}-err" role="alert"><Icon name="alert" size={16} />{error}</span>
	{/if}
</div>

<style>
	.req {
		color: var(--bad);
		margin-left: 0.15rem;
	}
</style>
