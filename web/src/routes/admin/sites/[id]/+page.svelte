<script lang="ts">
	import { enhance } from '$app/forms';
	import Empty from '#lib/components/Empty.svelte';
	import Field from '#lib/components/Field.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import LocationPicker from '#lib/components/LocationPicker.svelte';
	import { messages } from '#lib/messages.ts';
	import {
		parseNumber,
		slugify,
		validateEmail,
		validateSite,
		type Errors
	} from '#lib/validation.ts';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	const initial = $derived(
		(form && 'values' in form && form.values) || {
			name: data.site?.name ?? '',
			slug: data.site?.slug ?? '',
			lat: data.site ? String(data.site.lat) : '',
			lon: data.site ? String(data.site.lon) : '',
			tz: data.site?.tz ?? 'Asia/Jakarta',
			is_public: data.site?.is_public ?? false
		}
	);

	// Local copies so typing, the map and the slug suggestion can all drive each other.
	let name = $state('');
	let slug = $state('');
	let lat = $state('');
	let lon = $state('');
	let tz = $state('');
	let isPublic = $state(false);
	let slugTouched = $state(false);
	let errors = $state<Errors>({});
	let busy = $state(false);
	let opEmail = $state('');
	let opError = $state<string | undefined>();

	$effect(() => {
		({ name, slug, lat, lon, tz, is_public: isPublic } = initial);
		errors = (form && 'errors' in form && (form.errors as Errors)) || {};
		slugTouched = !!data.site;
	});
	$effect(() => {
		opEmail = (form && 'operatorEmail' in form && form.operatorEmail) || '';
		opError = (form && 'operatorError' in form && form.operatorError) || undefined;
	});

	const latN = $derived(parseNumber(lat));
	const lonN = $derived(parseNumber(lon));
	const isNew = $derived(!data.site);
	const formError = $derived(form && 'formError' in form ? (form.formError as string) : null);

	function onName() {
		if (isNew && !slugTouched) slug = slugify(name);
	}

	function pick(la: number, lo: number) {
		lat = String(la);
		lon = String(lo);
	}
</script>

<svelte:head
	><title>{isNew ? messages.sites.newTitle : messages.sites.editTitle} · {messages.appName}</title
	></svelte:head
>

<p class="crumb"><a href="/admin/sites">← {messages.sites.title}</a></p>
<h1>{isNew ? messages.sites.newTitle : `${messages.sites.editTitle}: ${data.site?.name}`}</h1>

{#if !data.isAdmin}
	<Empty icon="lock" title={messages.admin.adminOnlyPage} body={messages.admin.roleHint.operator}>
		<a class="btn" href="/admin/sites">{messages.back}</a>
	</Empty>
{:else}
	{#if data.saved || (form && 'saved' in form && form.saved)}
		<p class="notice ok" role="status">
			<Icon name="check" size={20} />
			{isNew ? messages.sites.created : messages.sites.updated}
		</p>
	{/if}
	{#if formError}<p class="notice bad" role="alert">
			<Icon name="alert" size={20} />
			{formError}
		</p>{/if}

	<form
		method="POST"
		action="?/save"
		class="card stack"
		novalidate
		use:enhance={({ cancel }) => {
			const v = validateSite({ name, slug, lat, lon, tz, is_public: isPublic });
			if (!v.ok) {
				errors = v.errors;
				cancel();
				return;
			}
			errors = {};
			busy = true;
			return async ({ update }) => {
				await update({ reset: false });
				busy = false;
			};
		}}
	>
		<div class="form-grid">
			<Field
				id="name"
				label={messages.sites.fieldName}
				hint={messages.sites.fieldNameHint}
				error={errors.name}
				required
			>
				{#snippet control(a)}
					<input
						{...a}
						type="text"
						name="name"
						bind:value={name}
						oninput={onName}
						maxlength="120"
						autocomplete="off"
					/>
				{/snippet}
			</Field>
			<Field
				id="slug"
				label={messages.sites.fieldSlug}
				hint={messages.sites.fieldSlugHint}
				error={errors.slug}
				required
			>
				{#snippet control(a)}
					<input
						{...a}
						type="text"
						name="slug"
						bind:value={slug}
						oninput={() => (slugTouched = true)}
						maxlength="63"
						autocomplete="off"
						spellcheck="false"
					/>
				{/snippet}
			</Field>
		</div>

		<fieldset>
			<legend>{messages.sites.pickLocation}</legend>
			<p class="muted">{messages.sites.pickLocationHint}</p>
			<LocationPicker
				lat={latN}
				lon={lonN}
				tileUrl={data.tileUrl}
				attribution={data.attribution}
				onpick={pick}
			/>
			<div class="form-grid coords">
				<Field id="lat" label={messages.sites.fieldLat} error={errors.lat} required>
					{#snippet control(a)}
						<input
							{...a}
							type="text"
							name="lat"
							bind:value={lat}
							inputmode="decimal"
							autocomplete="off"
						/>
					{/snippet}
				</Field>
				<Field id="lon" label={messages.sites.fieldLon} error={errors.lon} required>
					{#snippet control(a)}
						<input
							{...a}
							type="text"
							name="lon"
							bind:value={lon}
							inputmode="decimal"
							autocomplete="off"
						/>
					{/snippet}
				</Field>
			</div>
		</fieldset>

		<div class="form-grid">
			<Field
				id="tz"
				label={messages.sites.fieldTz}
				hint={messages.sites.fieldTzHint}
				error={errors.tz}
				required
			>
				{#snippet control(a)}
					<input
						{...a}
						type="text"
						name="tz"
						bind:value={tz}
						list="zones"
						autocomplete="off"
						spellcheck="false"
					/>
				{/snippet}
			</Field>
			<datalist id="zones">
				<option value="Asia/Jakarta">WIB</option>
				<option value="Asia/Pontianak">WIB</option>
				<option value="Asia/Makassar">WITA</option>
				<option value="Asia/Jayapura">WIT</option>
				<option value="UTC"></option>
			</datalist>
		</div>

		<div class="check">
			<input
				id="is_public"
				type="checkbox"
				name="is_public"
				bind:checked={isPublic}
				aria-describedby="pub-hint"
			/>
			<div>
				<label for="is_public"><strong>{messages.sites.fieldPublic}</strong></label>
				<p class="hint muted" id="pub-hint">{messages.sites.fieldPublicHint}</p>
				{#if isPublic}<p class="notice warn" role="status">
						<Icon name="alert" size={18} />
						{messages.sites.publicWarn}
					</p>{/if}
			</div>
		</div>

		<div class="row">
			<button class="btn" type="submit" disabled={busy}>{messages.save}</button>
			<a class="btn secondary" href="/admin/sites">{messages.cancel}</a>
			{#if data.site}
				<a class="btn secondary" href="/s/{data.site.slug}">{messages.sites.viewPublic}</a>
			{/if}
		</div>
	</form>

	{#if data.site}
		<section class="card" aria-labelledby="op-title">
			<h2 id="op-title">{messages.sites.operatorsTitle}</h2>
			<p class="muted">{messages.sites.operatorsHint}</p>
			{#if form && 'operatorAdded' in form && form.operatorAdded}
				<p class="notice ok" role="status">
					<Icon name="check" size={20} />
					{messages.sites.operatorAdded}
				</p>
			{/if}
			<form
				method="POST"
				action="?/operator"
				class="row opform"
				novalidate
				use:enhance={({ cancel }) => {
					const v = validateEmail(opEmail);
					if (!v.ok) {
						opError = v.errors.email;
						cancel();
						return;
					}
					opError = undefined;
					return async ({ update }) => update({ reset: false });
				}}
			>
				<div class="grow">
					<Field id="op-email" label={messages.sites.operatorEmail} error={opError}>
						{#snippet control(a)}
							<input {...a} type="email" name="email" bind:value={opEmail} autocomplete="off" />
						{/snippet}
					</Field>
				</div>
				<button class="btn secondary" type="submit">{messages.sites.operatorAdd}</button>
			</form>
		</section>
	{/if}
{/if}

<style>
	.crumb {
		margin: 0 0 0.5rem;
	}
	h1 {
		margin-bottom: 1rem;
	}
	.coords {
		margin-top: 1rem;
	}
	.hint {
		margin: 0.15rem 0 0;
		font-size: 0.9rem;
	}
	.opform {
		align-items: end;
	}
	.grow {
		flex: 1 1 16rem;
	}
</style>
