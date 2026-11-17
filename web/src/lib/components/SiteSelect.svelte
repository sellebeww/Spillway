<script lang="ts">
	import { messages } from '#lib/messages.ts';
	import type { Site } from '#lib/types.ts';

	// A GET form: works without JavaScript; with it, picking a site submits at once.
	let {
		sites,
		selected,
		name = 'site',
		extra = {}
	}: {
		sites: Site[];
		selected: string | null;
		name?: string;
		extra?: Record<string, string>;
	} = $props();

	let form: HTMLFormElement;
</script>

<form method="GET" class="row picker" bind:this={form}>
	{#each Object.entries(extra) as [k, v] (k)}
		<input type="hidden" name={k} value={v} />
	{/each}
	<div class="field grow">
		<label for="site-select">{messages.admin.pickSite}</label>
		<select id="site-select" {name} onchange={() => form.requestSubmit()}>
			{#each sites as s (s.id)}
				<option value={s.id} selected={s.id === selected}>{s.name}</option>
			{/each}
		</select>
	</div>
	<noscript
		><button class="btn secondary" type="submit">{messages.admin.pickSiteGo}</button></noscript
	>
</form>

<style>
	.picker {
		align-items: end;
	}
	.grow {
		flex: 1 1 16rem;
		max-width: 28rem;
	}
</style>
