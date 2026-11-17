<script lang="ts">
	import { Tween } from 'svelte/motion';
	import { cubicOut } from 'svelte/easing';
	import { formatNumber } from '#lib/format.ts';
	import { messages } from '#lib/messages.ts';
	import type { PublicThreshold } from '#lib/types.ts';

	// The big number is the information; the tank beside it is decoration (hidden from assistive tech).
	let {
		level,
		thresholds,
		stale
	}: { level: number | null; thresholds: PublicThreshold[]; stale: boolean } = $props();

	const reduce =
		typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
	const duration = reduce ? 0 : 900;
	const shown = new Tween(0, { duration, easing: cubicOut });
	const fill = new Tween(0, { duration, easing: cubicOut });

	const top = $derived(
		Math.max(...thresholds.map((t) => t.level_cm), (level ?? 0) * 1.1, 100) * 1.12
	);
	const frac = $derived(level === null ? 0 : Math.min(1, Math.max(0.02, level / top)));

	$effect(() => {
		shown.target = level ?? 0;
		fill.target = frac;
	});

	// Tank geometry (viewBox 120 x 220): inner area x 14..106, y 12..208.
	const yOf = (f: number) => 208 - f * 196;
</script>

<div class="gauge" class:stale>
	<div class="num" aria-live="off">
		<span class="v">{level === null ? '-' : formatNumber(shown.current)}</span>
		<span class="u">cm</span>
	</div>
	<svg viewBox="0 0 120 220" aria-hidden="true" focusable="false" class="tank">
		<defs>
			<clipPath id="tank-clip"><rect x="14" y="12" width="92" height="196" rx="10" /></clipPath>
			<linearGradient id="water-g" x1="0" y1="0" x2="0" y2="1">
				<stop offset="0" stop-color="var(--brand)" stop-opacity="0.85" />
				<stop offset="1" stop-color="var(--brand-strong)" stop-opacity="0.95" />
			</linearGradient>
		</defs>
		<rect x="14" y="12" width="92" height="196" rx="10" fill="var(--surface-2)" />
		<g clip-path="url(#tank-clip)">
			<g transform="translate(0 {yOf(fill.current)})">
				<g class="wave back"
					><path
						d="M0 8 Q 23 0 46 8 T 92 8 T 138 8 T 184 8 T 230 8 V 230 H 0 Z"
						fill="var(--brand)"
						opacity="0.35"
					/></g
				>
				<g class="wave front"
					><path
						d="M0 8 Q 23 16 46 8 T 92 8 T 138 8 T 184 8 T 230 8 V 230 H 0 Z"
						fill="url(#water-g)"
					/></g
				>
			</g>
			{#each thresholds as t (t.severity + ':' + t.level_cm)}
				{@const y = yOf(Math.min(1, t.level_cm / top))}
				<line
					x1="14"
					x2="106"
					y1={y}
					y2={y}
					stroke="var(--sev{t.severity === 3 ? '3-bg' : t.severity})"
					stroke-width="2.5"
					stroke-dasharray="6 4"
				/>
			{/each}
		</g>
		<rect
			x="14"
			y="12"
			width="92"
			height="196"
			rx="10"
			fill="none"
			stroke="var(--ink-2)"
			stroke-width="2.5"
		/>
	</svg>
	<span class="sr-only">{messages.site.referenceNote}</span>
</div>

<style>
	.gauge {
		display: flex;
		align-items: center;
		gap: clamp(1rem, 4vw, 2rem);
	}
	.num {
		display: flex;
		align-items: baseline;
		gap: 0.4rem;
		font-variant-numeric: tabular-nums;
	}
	.v {
		font-size: clamp(3.4rem, 14vw, 6rem);
		font-weight: 850;
		line-height: 1;
		letter-spacing: -0.03em;
		background: linear-gradient(180deg, var(--ink), var(--brand));
		-webkit-background-clip: text;
		background-clip: text;
		color: transparent;
	}
	.u {
		font-size: clamp(1.2rem, 4vw, 1.8rem);
		font-weight: 700;
		color: var(--ink-2);
	}
	.tank {
		width: clamp(3.6rem, 12vw, 5.5rem);
		flex: none;
		filter: drop-shadow(0 6px 10px rgb(10 29 43 / 0.12));
	}
	.wave {
		width: 230px;
		animation: wave-slide 5s linear infinite;
	}
	.wave.back {
		animation-duration: 8s;
		animation-direction: reverse;
	}
	.stale .v {
		background: none;
		color: var(--ink-2);
		text-decoration: line-through;
		text-decoration-thickness: 3px;
		text-decoration-color: var(--warn);
	}
	.stale .tank {
		filter: grayscale(1);
		opacity: 0.6;
	}
	.stale .wave {
		animation-play-state: paused;
	}
</style>
