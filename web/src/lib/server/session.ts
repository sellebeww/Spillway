// Copyright (c) 2026 Vassel Goleyu. MIT License.
// Server-side sessions: the browser only holds an opaque random id in an httpOnly cookie. Tokens
// never reach client JavaScript.
//
// shortcut: in-memory store. A restart signs everyone out and it does not span several instances;
// move to Redis (already part of the stack) before running more than one web process.

import { randomToken } from './oidc';

export interface Session {
	id: string;
	accessToken: string;
	refreshToken?: string;
	/** Access token expiry, epoch ms. */
	expiresAt: number;
	idToken?: string;
	/** Display only (from the ID token); authorization always comes from the API. */
	label?: string;
	createdAt: number;
}

export type NewSession = Omit<Session, 'id' | 'createdAt'>;

export class SessionStore {
	private readonly map = new Map<string, Session>();

	constructor(
		private readonly maxAgeMs = 12 * 3_600_000,
		private readonly maxSessions = 5000,
		private readonly now: () => number = Date.now
	) {}

	create(data: NewSession): Session {
		this.sweep();
		if (this.map.size >= this.maxSessions) {
			// Oldest first: Map keeps insertion order.
			const oldest = this.map.keys().next().value;
			if (oldest !== undefined) this.map.delete(oldest);
		}
		const s: Session = { ...data, id: randomToken(32), createdAt: this.now() };
		this.map.set(s.id, s);
		return s;
	}

	get(id: string | undefined): Session | undefined {
		if (!id) return undefined;
		const s = this.map.get(id);
		if (!s) return undefined;
		if (this.now() - s.createdAt > this.maxAgeMs) {
			this.map.delete(id);
			return undefined;
		}
		return s;
	}

	delete(id: string | undefined): void {
		if (id) this.map.delete(id);
	}

	get size(): number {
		return this.map.size;
	}

	private sweep(): void {
		const t = this.now();
		for (const [id, s] of this.map) if (t - s.createdAt > this.maxAgeMs) this.map.delete(id);
	}
}

export interface Pending {
	verifier: string;
	nonce: string;
	next: string;
}

/** Login attempts in flight, keyed by `state`; each is single use and short lived. */
export class PendingStore {
	private readonly map = new Map<string, Pending & { at: number }>();

	constructor(
		private readonly ttlMs = 10 * 60_000,
		private readonly max = 1000,
		private readonly now: () => number = Date.now
	) {}

	put(state: string, p: Pending): void {
		const t = this.now();
		for (const [k, v] of this.map) if (t - v.at > this.ttlMs) this.map.delete(k);
		if (this.map.size >= this.max) {
			const oldest = this.map.keys().next().value;
			if (oldest !== undefined) this.map.delete(oldest);
		}
		this.map.set(state, { ...p, at: t });
	}

	/** Returns and removes the entry: a state value works exactly once. */
	take(state: string | null | undefined): Pending | undefined {
		if (!state) return undefined;
		const v = this.map.get(state);
		this.map.delete(state);
		if (!v || this.now() - v.at > this.ttlMs) return undefined;
		return { verifier: v.verifier, nonce: v.nonce, next: v.next };
	}
}
