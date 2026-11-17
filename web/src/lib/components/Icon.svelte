<script lang="ts">
	// Inline icons. Status is always shown as icon shape plus words, never colour alone.
	let { name, size = 18, label }: { name: string; size?: number; label?: string } = $props();

	const icons: Record<string, string[]> = {
		check: ['M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0z', 'M8 12.5l2.7 2.7L16 9.5'],
		clock: ['M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0z', 'M12 7v5l3 2'],
		cross: ['M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0z', 'M9 9l6 6', 'M15 9l-6 6'],
		triangle: ['M12 3.2 2.6 19.8h18.8L12 3.2z', 'M12 10v4.2', 'M12 17.3h.01'],
		diamond: ['M12 2.6 21.4 12 12 21.4 2.6 12 12 2.6z', 'M12 8v5', 'M12 16.2h.01'],
		octagon: [
			'M8.2 2.8h7.6l5.4 5.4v7.6l-5.4 5.4H8.2l-5.4-5.4V8.2l5.4-5.4z',
			'M12 7.5v5',
			'M12 15.8h.01'
		],
		alert: ['M12 3.2 2.6 19.8h18.8L12 3.2z', 'M12 10v4.2', 'M12 17.3h.01'],
		info: ['M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0z', 'M12 11v5', 'M12 8h.01'],
		water: ['M12 3s6.5 6.6 6.5 11.2a6.5 6.5 0 0 1-13 0C5.5 9.6 12 3 12 3z'],
		copy: ['M9 9h11v11H9z', 'M5 15V5a1 1 0 0 1 1-1h10'],
		up: ['M12 19V5', 'M5 12l7-7 7 7'],
		down: ['M12 5v14', 'M19 12l-7 7-7-7'],
		flat: ['M5 12h14', 'M15 8l4 4-4 4'],
		plus: ['M12 5v14', 'M5 12h14'],
		trash: ['M4 7h16', 'M9 7V4h6v3', 'M6 7l1 13h10l1-13'],
		external: [
			'M14 4h6v6',
			'M20 4l-9 9',
			'M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5'
		],
		signal: ['M5 12a10 10 0 0 1 14 0', 'M8.5 15.5a5 5 0 0 1 7 0', 'M12 19h.01'],
		battery: ['M3 8h15v8H3z', 'M21 10v4'],
		pin: [
			'M12 21s7-6.2 7-11.5A7 7 0 0 0 5 9.5C5 14.8 12 21 12 21z',
			'M12 12a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5z'
		],
		lock: ['M6 11h12v9H6z', 'M8.5 11V8a3.5 3.5 0 0 1 7 0v3'],
		send: ['M21 3 10 14', 'M21 3l-7 18-4-7-7-4 18-7z'],
		logout: ['M10 4H5v16h5', 'M15 8l4 4-4 4', 'M9 12h10'],
		list: ['M8 6h12', 'M8 12h12', 'M8 18h12', 'M4 6h.01', 'M4 12h.01', 'M4 18h.01']
	};
</script>

<svg
	width={size}
	height={size}
	viewBox="0 0 24 24"
	fill="none"
	stroke="currentColor"
	stroke-width="2.1"
	stroke-linecap="round"
	stroke-linejoin="round"
	role={label ? 'img' : undefined}
	aria-label={label}
	aria-hidden={label ? undefined : 'true'}
	focusable="false"
>
	{#each icons[name] ?? [] as d (d)}
		<path {d} />
	{/each}
</svg>

<style>
	svg {
		flex: none;
		vertical-align: -0.2em;
	}
</style>
