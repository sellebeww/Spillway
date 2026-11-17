import { readFileSync } from 'node:fs';
import { expect, test } from 'vitest';
import { messages } from './messages';

test('safety statement is present and names the official agencies', () => {
	expect(messages.safety).toContain('BMKG');
	expect(messages.safety).toContain('BUKAN sistem peringatan dini resmi');
});

test('the document language matches the Indonesian UI (screen readers pick the voice from it)', () => {
	expect(readFileSync('src/app.html', 'utf8')).toMatch(/<html lang="id">/);
});
