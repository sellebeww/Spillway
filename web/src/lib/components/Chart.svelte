<script lang="ts">
	import { onMount } from 'svelte';
	import { buildChartOption, type Palette } from '#lib/chartOption.ts';
	import { messages } from '#lib/messages.ts';
	import type { Resolution } from '#lib/resolution.ts';
	import type { ChartPoint } from '#lib/series.ts';
	import type { PublicAlert, PublicThreshold } from '#lib/types.ts';

	let {
		points,
		res,
		tz,
		thresholds,
		alerts,
		fromMs,
		nowMs,
		label
	}: {
		points: ChartPoint[];
		res: Resolution;
		tz: string;
		thresholds: PublicThreshold[];
		alerts: PublicAlert[];
		fromMs: number;
		nowMs: number;
		label: string;
	} = $props();

	let el: HTMLDivElement;
	// eslint-disable-next-line @typescript-eslint/no-explicit-any
	let chart: any = null;
	let ready = $state(false);

	function palette(): Palette {
		const cs = getComputedStyle(document.documentElement);
		const v = (n: string) => cs.getPropertyValue(n).trim();
		return {
			ink: v('--ink'),
			ink2: v('--ink-2'),
			line: v('--line'),
			brand: v('--brand'),
			surface: v('--surface'),
			sev: { 1: v('--sev1'), 2: v('--sev2'), 3: v('--sev3-bg') }
		};
	}

	function draw() {
		if (!chart) return;
		chart.setOption(
			buildChartOption({ points, res, tz, thresholds, alerts, fromMs, nowMs, palette: palette() }),
			{ notMerge: true }
		);
	}

	onMount(() => {
		let disposed = false;
		let ro: ResizeObserver | undefined;
		const mq = window.matchMedia('(prefers-color-scheme: dark)');
		const onScheme = () => draw();
		(async () => {
			const { echarts } = await import('#lib/echarts.ts'); // lazy: not in the initial bundle
			if (disposed) return;
			chart = echarts.init(el, undefined, { renderer: 'canvas' });
			ro = new ResizeObserver(() => chart?.resize());
			ro.observe(el);
			mq.addEventListener('change', onScheme);
			ready = true;
		})();
		return () => {
			disposed = true;
			ro?.disconnect();
			mq.removeEventListener('change', onScheme);
			chart?.dispose();
			chart = null;
		};
	});

	$effect(() => {
		// Redraw whenever any input changes.
		void [points, res, tz, thresholds, alerts, fromMs, nowMs, ready];
		if (ready) draw();
	});
</script>

<div class="chart" bind:this={el} role="img" aria-label={label}>
	{#if !ready}<div class="skeleton fill" aria-hidden="true"></div>{/if}
</div>
<span class="sr-only" aria-live="polite">{ready ? '' : messages.site.chartLoading}</span>

<style>
	.chart {
		position: relative;
		width: 100%;
		height: clamp(15rem, 48vw, 22rem);
	}
	.fill {
		position: absolute;
		inset: 0;
	}
</style>
