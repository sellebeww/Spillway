<script lang="ts">
	import { onDestroy } from 'svelte';
	import { messages } from '#lib/messages.ts';
	import Icon from './Icon.svelte';

	let { text, label = messages.copy }: { text: string; label?: string } = $props();
	let done = $state(false);
	let timer: ReturnType<typeof setTimeout> | undefined;

	async function copy() {
		try {
			await navigator.clipboard.writeText(text);
		} catch {
			// Clipboard API needs a secure context; fall back to selecting a temporary field.
			const ta = document.createElement('textarea');
			ta.value = text;
			ta.setAttribute('readonly', '');
			ta.style.position = 'fixed';
			ta.style.opacity = '0';
			document.body.appendChild(ta);
			ta.select();
			document.execCommand('copy');
			ta.remove();
		}
		done = true;
		clearTimeout(timer);
		timer = setTimeout(() => (done = false), 2200);
	}

	onDestroy(() => clearTimeout(timer));
</script>

<button type="button" class="btn secondary small" onclick={copy}>
	<Icon name={done ? 'check' : 'copy'} size={16} />
	{done ? messages.copied : label}
</button>
<span class="sr-only" aria-live="polite">{done ? messages.copied : ''}</span>
