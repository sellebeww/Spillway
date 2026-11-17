// Copyright (c) 2026 Vassel Goleyu. MIT License.
import { formatDateTime, formatNumber } from './format';
import { messages } from './messages';
import type { Resolution } from './resolution';
import type { ChartPoint } from './series';
import type { PublicAlert, PublicThreshold } from './types';

export interface Palette {
	ink: string;
	ink2: string;
	line: string;
	brand: string;
	surface: string;
	sev: Record<1 | 2 | 3, string>;
}

export interface ChartInput {
	points: ChartPoint[];
	res: Resolution;
	tz: string;
	thresholds: PublicThreshold[];
	alerts: PublicAlert[];
	nowMs: number;
	fromMs: number;
	palette: Palette;
}

const sevName = (s: number) => messages.severity[(s >= 1 && s <= 3 ? s : 1) as 1 | 2 | 3].label;

/** Short axis label in the site zone: time of day for short spans, date for long ones. */
export function axisLabel(ms: number, tz: string, spanMs: number): string {
	const full = formatDateTime(new Date(ms), tz); // "16 Nov 2026 18.05 WIB"
	const m = /^(\d+ \S+) \d{4} (\d\d\.\d\d)/.exec(full);
	if (!m) return full;
	return spanMs <= 36 * 3_600_000 ? m[2] : m[1];
}

/** ECharts option for the level chart: line (and min/max band for aggregates), threshold lines, alert spans. */
export function buildChartOption(i: ChartInput) {
	const { palette: p } = i;
	const aggregated = i.res !== 'raw';
	const span = Math.max(i.nowMs - i.fromMs, 1);
	const line = i.points.map((x) => [x.t, x.v]);

	const series: Record<string, unknown>[] = [];
	if (aggregated) {
		series.push(
			{
				name: messages.site.seriesMin,
				type: 'line',
				data: i.points.map((x) => [x.t, x.min ?? x.v]),
				stack: 'band',
				symbol: 'none',
				lineStyle: { opacity: 0 },
				silent: true,
				tooltip: { show: false }
			},
			{
				name: messages.site.seriesMax,
				type: 'line',
				data: i.points.map((x) => [x.t, (x.max ?? x.v) - (x.min ?? x.v)]),
				stack: 'band',
				symbol: 'none',
				lineStyle: { opacity: 0 },
				areaStyle: { color: p.brand, opacity: 0.16 },
				silent: true,
				tooltip: { show: false }
			}
		);
	}
	series.push({
		name: aggregated ? messages.site.seriesAvg : messages.site.seriesLevel,
		type: 'line',
		data: line,
		showSymbol: line.length < 60,
		symbolSize: 6,
		smooth: false,
		sampling: 'lttb',
		lineStyle: { width: 2.6, color: p.brand },
		itemStyle: { color: p.brand },
		areaStyle: aggregated ? undefined : { color: p.brand, opacity: 0.12 },
		animationDuration: 900,
		animationEasing: 'cubicOut',
		markLine: {
			symbol: 'none',
			silent: true,
			animation: false,
			data: i.thresholds.map((t) => ({
				yAxis: t.level_cm,
				name: sevName(t.severity),
				lineStyle: { color: p.sev[t.severity], type: 'dashed', width: 2 },
				label: {
					show: true,
					position: 'insideEndTop',
					color: p.ink,
					fontWeight: 700,
					formatter: `${sevName(t.severity)} ${formatNumber(t.level_cm)}`
				}
			}))
		},
		markArea: {
			silent: true,
			animation: false,
			data: i.alerts
				.map((a) => {
					const from = Date.parse(a.opened_at);
					const to = a.closed_at ? Date.parse(a.closed_at) : i.nowMs;
					return { a, from, to };
				})
				.filter((x) => x.to >= i.fromMs && x.from <= i.nowMs)
				.map(({ a, from, to }) => [
					{
						xAxis: Math.max(from, i.fromMs),
						name: messages.site.alertMarker(sevName(a.severity)),
						itemStyle: { color: p.sev[a.severity], opacity: 0.14 },
						label: {
							show: true,
							position: 'insideTop',
							color: p.ink,
							fontWeight: 700,
							fontSize: 11
						}
					},
					{ xAxis: Math.min(to, i.nowMs) }
				])
		}
	});

	return {
		animation: true,
		aria: { enabled: true, decal: { show: false } },
		grid: { left: 8, right: 14, top: 28, bottom: 8, containLabel: true },
		textStyle: { color: p.ink2 },
		tooltip: {
			trigger: 'axis',
			confine: true,
			backgroundColor: p.surface,
			borderColor: p.line,
			textStyle: { color: p.ink },
			formatter: (params: Array<{ value: [number, number]; seriesName: string }>) => {
				const pt = i.points.find((x) => x.t === params[0]?.value?.[0]);
				if (!pt) return '';
				const head = formatDateTime(new Date(pt.t), i.tz);
				const body = aggregated
					? `${messages.site.seriesAvg}: ${formatNumber(pt.v)} cm<br>${messages.site.seriesMin}: ${formatNumber(pt.min ?? pt.v)} cm<br>${messages.site.seriesMax}: ${formatNumber(pt.max ?? pt.v)} cm`
					: `${messages.site.seriesLevel}: ${formatNumber(pt.v)} cm`;
				return `<strong>${head}</strong><br>${body}`;
			}
		},
		xAxis: {
			type: 'time',
			min: i.fromMs,
			max: i.nowMs,
			axisLine: { lineStyle: { color: p.line } },
			axisLabel: {
				color: p.ink2,
				hideOverlap: true,
				formatter: (v: number) => axisLabel(v, i.tz, span)
			},
			splitLine: { show: false }
		},
		yAxis: {
			type: 'value',
			scale: true,
			// Keep the nearest threshold above the data in view, so the distance to it is visible.
			max: (v: { max: number }) => {
				const above = i.thresholds.map((t) => t.level_cm).filter((l) => l > v.max);
				return above.length ? Math.max(v.max, Math.min(...above)) : v.max;
			},
			name: 'cm',
			nameTextStyle: { color: p.ink2 },
			axisLabel: { color: p.ink2 },
			splitLine: { lineStyle: { color: p.line, type: 'dotted' } }
		},
		series
	};
}
