<script lang="ts">
	import { enhance } from '$app/forms';
	import Empty from '#lib/components/Empty.svelte';
	import Field from '#lib/components/Field.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import SeverityBadge from '#lib/components/SeverityBadge.svelte';
	import SiteSelect from '#lib/components/SiteSelect.svelte';
	import { messages } from '#lib/messages.ts';
	import { validateRule, type Errors } from '#lib/validation.ts';
	import type { Rule } from '#lib/types.ts';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	type Tier = { value: string; severity: string };
	let editing = $state<string>(''); // rule id, '' means a new rule
	let open = $state(false);
	let type = $state<'THRESHOLD' | 'RATE_OF_RISE'>('THRESHOLD');
	let tiers = $state<Tier[]>([{ value: '', severity: '1' }]);
	let hysteresis = $state('');
	let cooldown = $state('');
	let minQuality = $state('');
	let enabled = $state(true);
	let errors = $state<Errors>({});

	function startNew() {
		editing = '';
		type = 'THRESHOLD';
		tiers = [{ value: '', severity: '1' }];
		hysteresis = cooldown = minQuality = '';
		enabled = true;
		errors = {};
		open = true;
	}

	function startEdit(r: Rule) {
		editing = r.id;
		type = r.type;
		tiers = r.tiers.map((t) => ({ value: String(t.value), severity: String(t.severity) }));
		hysteresis = r.type === 'THRESHOLD' ? String(r.hysteresis_cm) : '';
		cooldown = String(Math.round(r.cooldown_s / 60));
		minQuality = String(r.min_quality);
		enabled = r.enabled;
		errors = {};
		open = true;
	}

	$effect(() => {
		if (form && 'values' in form && form.values) {
			const v = form.values;
			editing = (form.ruleId as string) || '';
			type = v.type === 'RATE_OF_RISE' ? 'RATE_OF_RISE' : 'THRESHOLD';
			tiers = v.tiers.length ? v.tiers.map((t) => ({ ...t })) : [{ value: '', severity: '1' }];
			hysteresis = v.hysteresis_cm;
			cooldown = v.cooldown_min;
			minQuality = v.min_quality;
			enabled = v.enabled;
			errors = (form.errors as Errors) ?? {};
			open = true;
		}
	});

	const unit = $derived(
		type === 'THRESHOLD' ? messages.rules.unitTHRESHOLD : messages.rules.unitRATE_OF_RISE
	);
	const formError = $derived(form && 'formError' in form ? (form.formError as string) : null);
	const sev = (n: number) => messages.severity[(n >= 1 && n <= 3 ? n : 1) as 1 | 2 | 3].label;

	function addTier() {
		if (tiers.length < 3)
			tiers = [
				...tiers,
				{ value: '', severity: String(Math.min(3, Number(tiers.at(-1)?.severity ?? 0) + 1)) }
			];
	}
	function removeTier(i: number) {
		if (tiers.length > 1) tiers = tiers.filter((_, k) => k !== i);
	}
</script>

<svelte:head
	><title>{messages.rules.title} · {messages.admin.title} · {messages.appName}</title></svelte:head
>

<div class="row between head">
	<div>
		<h1>{messages.rules.title}</h1>
		<p class="muted">{messages.rules.intro}</p>
	</div>
	{#if data.site}<button class="btn" type="button" onclick={startNew}
			><Icon name="plus" size={18} />{messages.rules.new}</button
		>{/if}
</div>

{#if !data.site}
	<Empty title={messages.admin.noSites} body={messages.admin.noSitesHint} />
{:else}
	<SiteSelect sites={data.sites} selected={data.site.id} />
	{#if data.saved}<p class="notice ok" role="status">
			<Icon name="check" size={20} />
			{messages.rules.saved}
		</p>{/if}
	{#if formError && !open}<p class="notice bad" role="alert">{formError}</p>{/if}

	{#if open}
		<section class="card" aria-labelledby="rule-form-title">
			<h2 id="rule-form-title">{editing ? messages.rules.editTitle : messages.rules.newTitle}</h2>
			{#if formError}<p class="notice bad" role="alert">
					<Icon name="alert" size={20} />
					{formError}
				</p>{/if}
			<form
				method="POST"
				action="?/save"
				class="stack"
				novalidate
				data-testid="rule-form"
				use:enhance={({ cancel }) => {
					const v = validateRule({
						type,
						tiers,
						hysteresis_cm: hysteresis,
						cooldown_min: cooldown,
						min_quality: minQuality,
						enabled
					});
					if (!v.ok) {
						errors = v.errors;
						cancel();
						return;
					}
					errors = {};
					return async ({ update }) => update({ reset: false });
				}}
			>
				<input type="hidden" name="site" value={data.site.id} />
				<input type="hidden" name="rule" value={editing} />
				<input type="hidden" name="tier_count" value={tiers.length} />

				<fieldset>
					<legend>{messages.rules.type}</legend>
					<div class="types">
						{#each ['THRESHOLD', 'RATE_OF_RISE'] as t (t)}
							<label class="type" class:on={type === t}>
								<input
									type="radio"
									name="type"
									value={t}
									bind:group={type}
									disabled={!!editing && type !== t}
								/>
								<span
									>{t === 'THRESHOLD'
										? messages.rules.typeTHRESHOLD
										: messages.rules.typeRATE_OF_RISE}</span
								>
							</label>
						{/each}
					</div>
					<p class="muted help">
						{type === 'THRESHOLD'
							? messages.rules.typeTHRESHOLDHelp
							: messages.rules.typeRATE_OF_RISEHelp}
					</p>
				</fieldset>

				<fieldset>
					<legend>{messages.rules.tiers}</legend>
					<p class="muted help">{messages.rules.tiersHint}</p>
					{#if errors.tiers}<p class="err" role="alert">{errors.tiers}</p>{/if}
					<div class="tiers">
						{#each tiers as t, i (i)}
							<div class="tier">
								<Field
									id="tv{i}"
									label="{messages.rules.tierN(i + 1)}: {type === 'THRESHOLD'
										? messages.rules.tierValueTHRESHOLD
										: messages.rules.tierValueRATE_OF_RISE}"
									hint={unit}
									error={errors[`tiers.${i}.value`]}
									required
								>
									{#snippet control(a)}<input
											{...a}
											type="text"
											name="tier_value_{i}"
											bind:value={t.value}
											inputmode="decimal"
											autocomplete="off"
										/>{/snippet}
								</Field>
								<Field
									id="ts{i}"
									label={messages.rules.tierSeverity}
									error={errors[`tiers.${i}.severity`]}
									required
								>
									{#snippet control(a)}
										<select {...a} name="tier_severity_{i}" bind:value={t.severity}>
											{#each [1, 2, 3] as s (s)}<option value={String(s)}>{sev(s)}</option>{/each}
										</select>
									{/snippet}
								</Field>
								{#if tiers.length > 1}
									<button
										class="btn secondary small rm"
										type="button"
										onclick={() => removeTier(i)}
										aria-label="{messages.rules.removeTier} {i + 1}"
									>
										<Icon name="trash" size={16} />{messages.rules.removeTier}
									</button>
								{/if}
							</div>
						{/each}
					</div>
					{#if tiers.length < 3}<button class="btn secondary small" type="button" onclick={addTier}
							><Icon name="plus" size={16} />{messages.rules.addTier}</button
						>{/if}
				</fieldset>

				<div class="form-grid">
					{#if type === 'THRESHOLD'}
						<Field
							id="hys"
							label={messages.rules.hysteresis}
							hint={messages.rules.hysteresisHint}
							error={errors.hysteresis_cm}
						>
							{#snippet control(a)}<input
									{...a}
									type="text"
									name="hysteresis_cm"
									bind:value={hysteresis}
									inputmode="decimal"
									placeholder="5"
								/>{/snippet}
						</Field>
					{/if}
					<Field
						id="cool"
						label={messages.rules.cooldown}
						hint={messages.rules.cooldownHint}
						error={errors.cooldown_min}
					>
						{#snippet control(a)}<input
								{...a}
								type="text"
								name="cooldown_min"
								bind:value={cooldown}
								inputmode="numeric"
								placeholder="30"
							/>{/snippet}
					</Field>
					<Field
						id="mq"
						label={messages.rules.minQuality}
						hint={messages.rules.minQualityHint}
						error={errors.min_quality}
					>
						{#snippet control(a)}<input
								{...a}
								type="text"
								name="min_quality"
								bind:value={minQuality}
								inputmode="decimal"
								placeholder="0,5"
							/>{/snippet}
					</Field>
				</div>

				<div class="check">
					<input id="enabled" type="checkbox" name="enabled" bind:checked={enabled} />
					<div>
						<label for="enabled"><strong>{messages.rules.enabled}</strong></label>
						<p class="muted help">{messages.rules.enabledHint}</p>
					</div>
				</div>

				<div class="row">
					<button class="btn" type="submit">{messages.save}</button>
					<button class="btn secondary" type="button" onclick={() => (open = false)}
						>{messages.cancel}</button
					>
				</div>
			</form>
		</section>
	{/if}

	{#if data.loadError}
		<Empty icon="signal" tone="bad" title={data.loadError}
			><a class="btn" href="?site={data.site.id}">{messages.retry}</a></Empty
		>
	{:else if data.rules.length === 0}
		<Empty title={messages.rules.empty} body={messages.rules.emptyHint} />
	{:else}
		<ul class="rules">
			{#each data.rules as r, i (r.id)}
				<li class="card reveal" style="--i: {i}">
					<div class="row between">
						<h2>
							{r.type === 'THRESHOLD'
								? messages.rules.typeTHRESHOLD
								: messages.rules.typeRATE_OF_RISE}
						</h2>
						<span class="badge" class:ok={r.enabled} class:idle={!r.enabled}>
							<Icon name={r.enabled ? 'check' : 'cross'} size={14} />{r.enabled
								? messages.rules.stateEnabled
								: messages.rules.stateDisabled}
						</span>
					</div>
					<ul class="tl">
						{#each r.tiers as t (t.severity)}
							<li>
								<SeverityBadge severity={t.severity} quiet />
								<span
									>{messages.rules.tierLine(
										t.value,
										r.type === 'THRESHOLD'
											? messages.rules.unitTHRESHOLD
											: messages.rules.unitRATE_OF_RISE,
										sev(t.severity)
									)}</span
								>
							</li>
						{/each}
					</ul>
					<p class="muted">
						{messages.rules.settingsLine(
							r.type === 'THRESHOLD' ? r.hysteresis_cm : null,
							Math.round(r.cooldown_s / 60),
							r.min_quality
						)}
					</p>
					<div class="row">
						<button class="btn secondary small" type="button" onclick={() => startEdit(r)}
							>{messages.edit}</button
						>
						<form method="POST" action="?/toggle" use:enhance>
							<input type="hidden" name="rule" value={r.id} />
							<input type="hidden" name="site" value={data.site.id} />
							<button class="btn secondary small" type="submit"
								>{r.enabled ? messages.rules.stateDisabled : messages.rules.stateEnabled}</button
							>
						</form>
					</div>
				</li>
			{/each}
		</ul>
	{/if}
{/if}

<style>
	.head {
		margin-bottom: 1rem;
		align-items: end;
	}
	h1 {
		margin: 0;
	}
	.head p {
		margin: 0.25rem 0 0;
	}
	section.card {
		margin: 1rem 0;
	}
	.types {
		display: flex;
		gap: 0.6rem;
		flex-wrap: wrap;
	}
	.type {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
		min-height: 2.75rem;
		padding: 0 1rem;
		border: 1.5px solid var(--ink-2);
		border-radius: var(--radius-sm);
		font-weight: 650;
		cursor: pointer;
	}
	.type.on {
		background: var(--brand);
		border-color: var(--brand);
		color: var(--on-brand);
	}
	.type input {
		accent-color: var(--on-brand);
	}
	.type:focus-within {
		outline: 3px solid var(--focus);
		outline-offset: 2px;
	}
	.help {
		margin: 0.5rem 0 0;
		font-size: 0.92rem;
	}
	.err {
		color: var(--bad);
		font-weight: 600;
		margin: 0.4rem 0;
	}
	.tiers {
		display: grid;
		gap: 1rem;
		margin: 0.75rem 0;
	}
	.tier {
		display: grid;
		gap: 0.75rem;
		grid-template-columns: repeat(auto-fit, minmax(min(100%, 14rem), 1fr));
		align-items: start;
		padding: 0.75rem;
		border-radius: var(--radius-sm);
		background: var(--surface-2);
		animation: rise 0.3s var(--ease) both;
	}
	.rm {
		align-self: end;
	}
	.rules {
		list-style: none;
		margin: 1rem 0 0;
		padding: 0;
		display: grid;
		gap: 1rem;
	}
	.rules h2 {
		margin: 0;
		font-size: 1.1rem;
	}
	.rules .card {
		margin-top: 0;
	}
	.tl {
		list-style: none;
		margin: 0.75rem 0;
		padding: 0;
		display: grid;
		gap: 0.4rem;
	}
	.tl li {
		display: flex;
		gap: 0.6rem;
		align-items: center;
		flex-wrap: wrap;
	}
</style>
