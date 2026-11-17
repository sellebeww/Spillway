// Copyright (c) 2026 Vassel Goleyu. MIT License.
// See https://svelte.dev/docs/kit/types#app.d.ts
import type { Session } from '#lib/server/session.ts';

declare global {
	namespace App {
		interface Locals {
			session?: Session;
		}
	}
}

export {};
