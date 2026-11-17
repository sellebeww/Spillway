<script lang="ts">
	import { onMount } from 'svelte';
	import { formatDateTime, formatLevel } from '#lib/format.ts';
	import { loadMapLibre, rasterStyle, type MapLibreModule } from '#lib/map/loader.ts';
	import { messages } from '#lib/messages.ts';
	import type { PublicSiteFeature } from '#lib/types.ts';

	let {
		sites,
		tileUrl,
		attribution,
		onfail
	}: {
		sites: PublicSiteFeature[];
		tileUrl: string;
		attribution: string;
		onfail: () => void;
	} = $props();

	let container: HTMLDivElement;
	let ml: MapLibreModule | null = null;
	let map: import('maplibre-gl').Map | null = null;
	let ready = $state(false);
	let fitted = false;

	interface Entry {
		marker: import('maplibre-gl').Marker;
		el: HTMLButtonElement;
		popup: import('maplibre-gl').Popup;
	}
	// Imperative bookkeeping for DOM markers, not reactive state.
	// eslint-disable-next-line svelte/prefer-svelte-reactivity
	const entries = new Map<string, Entry>();

	const SVG = 'http://www.w3.org/2000/svg';
	const ICON: Record<string, string[]> = {
		check: ['M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0z', 'M8 12.5l2.7 2.7L16 9.5'],
		clock: ['M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0z', 'M12 7v5l3 2'],
		cross: ['M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0z', 'M9 9l6 6', 'M15 9l-6 6']
	};

	function icon(name: string): SVGSVGElement {
		const svg = document.createElementNS(SVG, 'svg');
		svg.setAttribute('viewBox', '0 0 24 24');
		svg.setAttribute('width', '18');
		svg.setAttribute('height', '18');
		svg.setAttribute('fill', 'none');
		svg.setAttribute('stroke', 'currentColor');
		svg.setAttribute('stroke-width', '2.2');
		svg.setAttribute('stroke-linecap', 'round');
		svg.setAttribute('stroke-linejoin', 'round');
		svg.setAttribute('aria-hidden', 'true');
		for (const d of ICON[name] ?? []) {
			const p = document.createElementNS(SVG, 'path');
			p.setAttribute('d', d);
			svg.appendChild(p);
		}
		return svg;
	}

	const tone = { ONLINE: 'ok', DELAYED: 'warn', OFFLINE: 'idle' } as const;

	// Everything below uses textContent and DOM nodes only: API strings are never parsed as HTML.
	function fillMarker(el: HTMLButtonElement, f: PublicSiteFeature) {
		const p = f.properties;
		const st = messages.status[p.status] ?? messages.status.OFFLINE;
		el.className = `marker ${tone[p.status] ?? 'idle'}${p.stale ? ' stale' : ''}`;
		el.setAttribute(
			'aria-label',
			messages.map.markerLabel(
				p.name,
				p.stale ? messages.stale.label : st.label,
				formatLevel(p.level_cm)
			)
		);
		el.replaceChildren(icon(st.icon));
		const t = document.createElement('span');
		t.className = 'ml-level';
		t.textContent = formatLevel(p.level_cm);
		el.appendChild(t);
		if (p.stale) {
			const s = document.createElement('span');
			s.className = 'ml-stale';
			s.textContent = messages.stale.label;
			el.appendChild(s);
		}
	}

	function popupContent(f: PublicSiteFeature): HTMLElement {
		const p = f.properties;
		const st = messages.status[p.status] ?? messages.status.OFFLINE;
		const box = document.createElement('div');
		box.className = 'pop';
		const h = document.createElement('h3');
		h.textContent = p.name;
		const lvl = document.createElement('p');
		lvl.className = 'pop-level';
		lvl.textContent = formatLevel(p.level_cm);
		const status = document.createElement('p');
		status.className = `pop-status ${tone[p.status] ?? 'idle'}`;
		status.append(icon(st.icon), ` ${st.label}`);
		box.append(h, lvl, status);
		if (p.stale) {
			const s = document.createElement('p');
			s.className = 'pop-stale';
			s.textContent = messages.stale.label;
			box.appendChild(s);
		}
		const when = document.createElement('p');
		when.className = 'pop-when';
		when.textContent = `${messages.map.updated}: ${p.updated_at ? formatDateTime(p.updated_at, p.tz) : messages.notAvailable}`;
		const a = document.createElement('a');
		a.href = `/s/${encodeURIComponent(p.slug)}`;
		a.className = 'pop-link';
		a.textContent = messages.map.openSite;
		box.append(when, a);
		return box;
	}

	function sync() {
		if (!ml || !map) return;
		// eslint-disable-next-line svelte/prefer-svelte-reactivity
		const seen = new Set<string>();
		for (const f of sites) {
			const slug = f.properties.slug;
			seen.add(slug);
			let e = entries.get(slug);
			if (!e) {
				const el = document.createElement('button');
				el.type = 'button';
				el.dataset.site = slug;
				const popup = new ml.Popup({ offset: 22, closeButton: true, maxWidth: '260px' });
				const marker = new ml.Marker({ element: el }).setLngLat(f.geometry.coordinates).addTo(map);
				el.addEventListener('click', (ev) => {
					ev.stopPropagation();
					const cur = sites.find((s) => s.properties.slug === slug);
					if (!cur || !map) return;
					popup.setLngLat(cur.geometry.coordinates).setDOMContent(popupContent(cur));
					if (!popup.isOpen()) popup.addTo(map);
				});
				e = { marker, el, popup };
				entries.set(slug, e);
			}
			fillMarker(e.el, f);
			e.marker.setLngLat(f.geometry.coordinates);
			if (e.popup.isOpen()) e.popup.setDOMContent(popupContent(f));
		}
		for (const [slug, e] of entries) {
			if (!seen.has(slug)) {
				e.popup.remove();
				e.marker.remove();
				entries.delete(slug);
			}
		}
		if (!fitted && sites.length) {
			fitted = true;
			const b = new ml.LngLatBounds();
			for (const f of sites) b.extend(f.geometry.coordinates);
			map.fitBounds(b, { padding: 70, maxZoom: 13, animate: false });
		}
	}

	onMount(() => {
		let disposed = false;
		let ro: ResizeObserver | undefined;
		(async () => {
			try {
				ml = await loadMapLibre();
				if (disposed) return;
				map = new ml.Map({
					container,
					style: rasterStyle(tileUrl, attribution || messages.map.attributionFallback),
					center: [113, -2],
					zoom: 4,
					attributionControl: { compact: true },
					cooperativeGestures: false
				});
				map.addControl(new ml.NavigationControl({ showCompass: false }), 'top-right');
				map.on('load', () => {
					ready = true;
					sync();
				});
				ro = new ResizeObserver(() => map?.resize());
				ro.observe(container);
			} catch {
				if (!disposed) onfail(); // no WebGL or the chunk could not load: the list below still works
			}
		})();
		return () => {
			disposed = true;
			ro?.disconnect();
			for (const e of entries.values()) e.popup.remove();
			entries.clear();
			map?.remove();
			map = null;
		};
	});

	$effect(() => {
		void sites;
		if (ready) sync();
	});
</script>

<div class="map-wrap">
	<div class="map" bind:this={container} role="region" aria-label={messages.map.mapLabel}></div>
	{#if !ready}
		<div class="loading skeleton" aria-hidden="true"><span>{messages.map.loadingMap}</span></div>
	{/if}
</div>

<style>
	.map-wrap {
		position: relative;
		border-radius: var(--radius);
		overflow: hidden;
		border: 1px solid var(--line);
		box-shadow: var(--shadow-lg);
		background: var(--surface-2);
	}
	.map {
		width: 100%;
		height: clamp(20rem, 62vh, 40rem);
	}
	.loading {
		position: absolute;
		inset: 0;
		display: grid;
		place-items: center;
		color: var(--ink-2);
		font-weight: 600;
		border-radius: 0;
	}

	/* Markers and popups are created from JavaScript, hence :global. */
	:global(.marker) {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
		min-height: 2.75rem;
		padding: 0 0.7rem 0 0.55rem;
		border-radius: 999px;
		border: 2.5px solid currentColor;
		background: var(--surface);
		font: 700 0.9rem/1 var(--font);
		cursor: pointer;
		box-shadow: var(--shadow);
		transition: transform 0.18s var(--ease);
		animation: rise 0.5s var(--ease) both;
	}
	:global(.marker:hover) {
		transform: scale(1.07);
	}
	:global(.marker.ok) {
		color: var(--ok);
	}
	:global(.marker.warn) {
		color: var(--warn);
	}
	:global(.marker.idle) {
		color: var(--idle);
	}
	:global(.marker .ml-level) {
		color: var(--ink);
	}
	:global(.marker.stale) {
		border-style: dashed;
		background: var(--warn-bg);
		animation:
			rise 0.5s var(--ease) both,
			pulse-ring 2.2s ease-out infinite 0.5s;
	}
	:global(.marker .ml-stale) {
		color: var(--warn);
		font-size: 0.72rem;
		letter-spacing: 0.04em;
	}
	:global(.maplibregl-popup-content) {
		background: var(--surface);
		color: var(--ink);
		border-radius: var(--radius-sm);
		padding: 0.9rem 1rem;
		font-family: var(--font);
		box-shadow: var(--shadow-lg);
	}
	:global(.maplibregl-popup-tip) {
		border-top-color: var(--surface) !important;
		border-bottom-color: var(--surface) !important;
	}
	:global(.maplibregl-popup-close-button) {
		color: var(--ink);
		font-size: 1.4rem;
		width: 2.75rem;
		height: 2.75rem;
	}
	:global(.pop h3) {
		margin: 0 1.6rem 0.2rem 0;
		font-size: 1.05rem;
	}
	:global(.pop p) {
		margin: 0.15rem 0;
	}
	:global(.pop-level) {
		font-size: 1.7rem;
		font-weight: 850;
	}
	:global(.pop-status) {
		font-weight: 700;
	}
	:global(.pop-status.ok) {
		color: var(--ok);
	}
	:global(.pop-status.warn) {
		color: var(--warn);
	}
	:global(.pop-status.idle) {
		color: var(--idle);
	}
	:global(.pop-stale) {
		display: inline-block;
		padding: 0.1rem 0.5rem;
		font-weight: 850;
		color: var(--warn);
		background: var(--warn-bg);
		border: 2px solid var(--warn);
		border-radius: 6px;
	}
	:global(.pop-when) {
		font-size: 0.88rem;
		color: var(--ink-2);
	}
	:global(.pop-link) {
		display: inline-block;
		margin-top: 0.4rem;
		font-weight: 700;
		color: var(--brand);
	}
</style>
