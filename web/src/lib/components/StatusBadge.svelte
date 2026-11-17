<script lang="ts">
	import { messages, type DeviceStatus } from '#lib/messages.ts';
	import Icon from './Icon.svelte';

	let { status }: { status: DeviceStatus } = $props();

	const tone: Record<DeviceStatus, string> = { ONLINE: 'ok', DELAYED: 'warn', OFFLINE: 'idle' };
	const info = $derived(messages.status[status] ?? messages.status.OFFLINE);
</script>

<span class="badge {tone[status] ?? 'idle'}" title={info.hint}>
	<Icon name={info.icon} size={16} />
	{info.label}
</span>
