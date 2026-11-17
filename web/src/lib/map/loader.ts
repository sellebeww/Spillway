// Copyright (c) 2026 Vassel Goleyu. MIT License.
// MapLibre is large. It is imported only here, only from components that run in the browser on the
// map and site editor pages, so the initial bundle of every other page never contains it.
import type * as MapLibre from 'maplibre-gl';

export type MapLibreModule = typeof MapLibre;

let cached: Promise<MapLibreModule> | null = null;

export function loadMapLibre(): Promise<MapLibreModule> {
	cached ??= (async () => {
		const [lib] = await Promise.all([
			import('maplibre-gl'),
			import('maplibre-gl/dist/maplibre-gl.css')
		]);
		return (lib as unknown as { default?: MapLibreModule }).default ?? (lib as MapLibreModule);
	})().catch((e) => {
		cached = null; // allow a retry after a failed (offline) chunk load
		throw e;
	});
	return cached;
}

/** A raster basemap style from the TILE_URL template ({z}/{x}/{y}); attribution is shown by the map control. */
export function rasterStyle(tileUrl: string, attribution: string) {
	return {
		version: 8 as const,
		sources: {
			base: { type: 'raster' as const, tiles: [tileUrl], tileSize: 256, maxzoom: 19, attribution }
		},
		layers: [{ id: 'base', type: 'raster' as const, source: 'base' }]
	};
}
