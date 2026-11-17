// Copyright (c) 2026 Vassel Goleyu. MIT License.

/**
 * Origin to allow in a CSP for a configured URL. A placeholder in the host, as in tile templates
 * like https://{s}.tile.example.com/{z}/{x}/{y}.png, becomes a wildcard label.
 */
export function originOf(url: string | undefined): string | null {
	if (!url) return null;
	const m = /^(https?):\/\/([^/?#]+)/.exec(url);
	if (!m) return null;
	const [, scheme, host] = m;
	if (!host.includes('{')) {
		try {
			return new URL(`${scheme}://${host}`).origin;
		} catch {
			return null;
		}
	}
	const rest = host.replace(/^\{[^}]*\}\./, '');
	return rest.includes('{') ? null : `${scheme}://*.${rest}`;
}

/** Only a t.me deep link is ever rendered as a link; anything else from the API is dropped. */
export function safeTelegramUrl(url: unknown): string | undefined {
	return typeof url === 'string' &&
		/^https:\/\/t\.me\/[A-Za-z0-9_]{3,64}(\?start=[A-Za-z0-9_-]{1,64})?$/.test(url)
		? url
		: undefined;
}

/**
 * Adds origins to directives of a Content-Security-Policy header value. SvelteKit builds the base
 * policy (with nonces); the API and tile origins are only known at run time.
 */
export function withExtraOrigins(csp: string, extra: Record<string, Array<string | null>>): string {
	const parts = csp
		.split(';')
		.map((s) => s.trim())
		.filter(Boolean);
	for (const [directive, origins] of Object.entries(extra)) {
		const add = origins.filter((o): o is string => !!o);
		if (!add.length) continue;
		const i = parts.findIndex((p) => p.split(/\s+/)[0] === directive);
		if (i < 0) {
			parts.push(`${directive} ${add.join(' ')}`);
			continue;
		}
		const have = parts[i].split(/\s+/);
		parts[i] = [...have, ...add.filter((o) => !have.includes(o))].join(' ');
	}
	return parts.join('; ');
}
