// Copyright (c) 2026 Vassel Goleyu. MIT License.
import { redirect } from '@sveltejs/kit';
import type { PageServerLoad } from './$types';

// /admin has no page of its own: it leads to the first section.
export const load: PageServerLoad = () => redirect(307, '/admin/sites');
