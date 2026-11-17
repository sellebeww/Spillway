<script lang="ts">
	import { messages, type Severity } from '#lib/messages.ts';
	import Icon from './Icon.svelte';

	let { severity, quiet = false }: { severity: number; quiet?: boolean } = $props();

	const level = $derived((severity >= 1 && severity <= 3 ? severity : 0) as Severity);
	const info = $derived(messages.severity[level]);
</script>

<span
	class="badge"
	class:sev1={level === 1}
	class:sev2={level === 2}
	class:sev3={level === 3}
	class:ok={level === 0}
	class:pulse={level === 3 && !quiet}
	title={info.hint}
>
	<Icon name={info.icon} size={16} />
	{info.label}
</span>

<style>
	.pulse {
		animation: pulse-ring 1.8s ease-out infinite;
		color: var(--sev3);
	}
</style>
