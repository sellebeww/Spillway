// Copyright (c) 2026 Vassel Goleyu. MIT License.
import { expect, test, type Page } from '@playwright/test';

// Heuristic accessibility guard on the real DOM (no axe dependency; see docs/AUDIT.md for what it does not cover).
async function problems(page: Page): Promise<string[]> {
	return page.evaluate(() => {
		const out: string[] = [];
		if (document.documentElement.lang !== 'id')
			out.push(`html lang is "${document.documentElement.lang}"`);
		if (document.querySelectorAll('main').length !== 1) out.push('page needs exactly one <main>');
		if (document.querySelectorAll('h1').length !== 1) out.push('page needs exactly one <h1>');
		let prev = 1;
		for (const h of document.querySelectorAll('h1,h2,h3,h4,h5,h6')) {
			const n = Number(h.tagName[1]);
			if (n > prev + 1) out.push(`heading jumps from h${prev} to h${n}: ${h.textContent?.trim()}`);
			prev = n;
		}
		const name = (el: Element): string => {
			const labelled = el.getAttribute('aria-labelledby');
			if (labelled)
				return labelled
					.split(/\s+/)
					.map((id) => document.getElementById(id)?.textContent ?? '')
					.join(' ')
					.trim();
			const direct = el.getAttribute('aria-label') ?? el.getAttribute('title');
			if (direct?.trim()) return direct.trim();
			const labels = (el as HTMLInputElement).labels;
			if (labels?.length)
				return Array.from(labels)
					.map((l) => l.textContent)
					.join(' ')
					.trim();
			return (el.textContent ?? '').trim();
		};
		for (const el of document.querySelectorAll('input:not([type=hidden]), select, textarea'))
			if (!name(el)) out.push(`form control without a name: ${el.outerHTML.slice(0, 80)}`);
		for (const el of document.querySelectorAll('button, a[href], [role=radio], [role=button]'))
			if (!name(el)) out.push(`interactive element without a name: ${el.outerHTML.slice(0, 80)}`);
		for (const img of document.querySelectorAll('img'))
			if (!img.hasAttribute('alt')) out.push(`image without alt: ${img.outerHTML.slice(0, 80)}`);
		return out;
	});
}

for (const path of ['/', '/s/kali-putih', '/s/kali-lama']) {
	test(`public page ${path} has names, landmarks and ordered headings`, async ({ page }) => {
		await page.goto(path);
		if (path === '/')
			await expect(page.locator('button.marker')).toHaveCount(3, { timeout: 30_000 });
		expect(await problems(page)).toEqual([]);
	});
}

test('admin pages have names, landmarks and ordered headings', async ({ page }) => {
	for (const path of ['/admin/calibration', '/admin/rules', '/admin/devices']) {
		await page.goto(path); // the mock OIDC provider signs the session in on the first visit
		await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
		expect(await problems(page), path).toEqual([]);
	}
});
