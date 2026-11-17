// Copyright (c) 2026 Vassel Goleyu. MIT License.
import { expect, test } from '@playwright/test';

const MOCK = 'http://127.0.0.1:4010';

test('map page loads against the mock API: list is server-rendered and markers appear', async ({
	page
}) => {
	await page.goto('/');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Peta tinggi muka air');
	await expect(page.getByRole('link', { name: /Kali Putih/ })).toBeVisible();
	await expect(page.getByText('BUKAN sistem peringatan dini resmi').first()).toBeVisible();
	// MapLibre needs WebGL; the markers prove the lazy map chunk loaded and drew the sites.
	await expect(page.locator('button.marker')).toHaveCount(3, { timeout: 30_000 });
	await page.locator('button.marker[data-site="kali-lama"]').click();
	await expect(page.locator('.pop-stale')).toHaveText('DATA TERTUNDA');
});

test('a stale site shows DATA TERTUNDA prominently, even without JavaScript', async ({
	browser
}) => {
	const ctx = await browser.newContext({
		javaScriptEnabled: false,
		baseURL: 'http://localhost:4173'
	});
	const page = await ctx.newPage();
	await page.goto('/s/kali-lama');
	const banner = page.getByRole('alert').filter({ hasText: 'DATA TERTUNDA' });
	await expect(banner).toBeVisible();
	await expect(banner).toContainText('Pembaruan terakhir');
	await ctx.close();
});

test('a fresh site does not show the stale label and offers the four ranges', async ({ page }) => {
	await page.goto('/s/kali-putih');
	await expect(page.getByText('DATA TERTUNDA')).toHaveCount(0);
	await expect(page.getByRole('radio')).toHaveCount(4);
	await expect(page.getByText('SIAGA').first()).toBeVisible();
});

test('calibration form rejects invalid input and sends nothing to the API', async ({
	page,
	request
}) => {
	await page.goto('/admin/calibration'); // not signed in: sent through the OIDC code flow with PKCE
	await expect(page.getByRole('heading', { name: 'Kalibrasi', level: 1 })).toBeVisible();
	const form = page.getByTestId('calibration-form');
	await form.getByLabel(/Offset \(cm\)/).fill('abc');
	await form.getByLabel(/Jarak valid minimum/).fill('4500');
	await form.getByLabel(/Jarak valid maksimum/).fill('250');
	await form.getByRole('button', { name: 'Simpan versi baru' }).click();
	await expect(form.getByText('Isi dengan angka.')).toBeVisible();
	await expect(form.getByText('Minimum harus lebih kecil dari maksimum.')).toBeVisible();
	const calls = await (await request.get(`${MOCK}/__calls`)).json();
	expect(calls).toHaveLength(0);

	// a valid value shows the preview from the last reading (raw 3000 mm) and is accepted
	await form.getByLabel(/Offset \(cm\)/).fill('450,5');
	await form.getByLabel(/Jarak valid minimum/).fill('250');
	await form.getByLabel(/Jarak valid maksimum/).fill('4500');
	await expect(page.getByTestId('preview')).toContainText('150,5 cm');
	await form.getByRole('button', { name: 'Simpan versi baru' }).click();
	await expect(page.getByText('Versi kalibrasi ditambahkan.')).toBeVisible();
	const after = await (await request.get(`${MOCK}/__calls`)).json();
	expect(after).toHaveLength(1);
	expect(after[0].body.offset_cm).toBe(450.5);
});
