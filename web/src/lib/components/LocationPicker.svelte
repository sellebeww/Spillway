<script lang="ts">
	import { onMount } from 'svelte';
	import { loadMapLibre, rasterStyle, type MapLibreModule } from '#lib/map/loader.ts';
	import { messages } from '#lib/messages.ts';

	// Click the map, or drag the pin, to choose a location. The lat/lon inputs next to it stay the
	// accessible way to do the same thing (keyboard, screen reader, no WebGL).
	let {
		lat,
		lon,
		tileUrl,
		attribution,
		onpick
	}: {
		lat: number | null;
		lon: number | null;
		tileUrl: string;
		attribution: string;
		onpick: (lat: number, lon: number) => void;
	} = $props();

	let container = $state<HTMLDivElement | null>(null);
	let ml: MapLibreModule | null = null;
	let map: import('maplibre-gl').Map | null = null;
	let marker: import('maplibre-gl').Marker | null = null;
	let ready = $state(false);
	let failed = $state(false);

	const round = (n: number) => Math.round(n * 1e6) / 1e6;

	function place(la: number, lo: number, move: boolean) {
		if (!ml || !map) return;
		if (!marker) {
			marker = new ml.Marker({ draggable: true, color: '#0a6a96' }).setLngLat([lo, la]).addTo(map);
			marker.on('dragend', () => {
				const p = marker!.getLngLat();
				onpick(round(p.lat), round(p.lng));
			});
		} else {
			marker.setLngLat([lo, la]);
		}
		if (move) map.easeTo({ center: [lo, la], zoom: Math.max(map.getZoom(), 12), duration: 500 });
	}

	onMount(() => {
		let disposed = false;
		let ro: ResizeObserver | undefined;
		(async () => {
			try {
				ml = await loadMapLibre();
				if (disposed) return;
				map = new ml.Map({
					container: container as HTMLDivElement,
					style: rasterStyle(tileUrl, attribution || messages.map.attributionFallback),
					center: lon !== null && lat !== null ? [lon, lat] : [113, -2],
					zoom: lon !== null && lat !== null ? 12 : 4,
					attributionControl: { compact: true }
				});
				map.addControl(new ml.NavigationControl({ showCompass: false }), 'top-right');
				map.on('click', (e) => {
					onpick(round(e.lngLat.lat), round(e.lngLat.lng));
				});
				map.on('load', () => {
					ready = true;
					if (lat !== null && lon !== null) place(lat, lon, false);
				});
				ro = new ResizeObserver(() => map?.resize());
				ro.observe(container as HTMLDivElement);
			} catch {
				failed = true;
			}
		})();
		return () => {
			disposed = true;
			ro?.disconnect();
			marker?.remove();
			map?.remove();
			map = marker = null;
		};
	});

	$effect(() => {
		const la = lat;
		const lo = lon;
		if (ready && la !== null && lo !== null && Number.isFinite(la) && Number.isFinite(lo))
			place(la, lo, true);
	});
</script>

{#if failed}
	<p class="muted">{messages.sites.pickLocationKeyboard}</p>
{:else}
	<div class="picker">
		<div class="map" bind:this={container} role="region" aria-label={messages.sites.mapLabel}></div>
		{#if !ready}<div class="skeleton cover" aria-hidden="true"></div>{/if}
	</div>
{/if}

<style>
	.picker {
		position: relative;
		border-radius: var(--radius-sm);
		overflow: hidden;
		border: 1.5px solid var(--ink-2);
	}
	.map {
		width: 100%;
		height: clamp(16rem, 45vh, 24rem);
		cursor: crosshair;
	}
	.cover {
		position: absolute;
		inset: 0;
		border-radius: 0;
	}
</style>
