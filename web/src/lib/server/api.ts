// Copyright (c) 2026 Vassel Goleyu. MIT License.
// Server-side client for the Spillway API. The bearer token is attached here, on the server.

export type ApiResult<T> =
	| { ok: true; status: number; data: T }
	| { ok: false; status: number; code: string; message: string };

export interface ApiRequest {
	base: string;
	path: string;
	method?: 'GET' | 'POST' | 'PUT';
	token?: string;
	body?: unknown;
	/** Real client address; the API rate-limits per IP and trusts X-Forwarded-For only behind a trusted proxy. */
	clientIp?: string;
	timeoutMs?: number;
	fetchFn?: typeof fetch;
}

export async function apiRequest<T>(r: ApiRequest): Promise<ApiResult<T>> {
	const headers: Record<string, string> = { accept: 'application/json' };
	if (r.token) headers.authorization = `Bearer ${r.token}`;
	if (r.clientIp) headers['x-forwarded-for'] = r.clientIp;
	if (r.body !== undefined) headers['content-type'] = 'application/json';
	let res: Response;
	try {
		res = await (r.fetchFn ?? fetch)(`${r.base.replace(/\/$/, '')}${r.path}`, {
			method: r.method ?? 'GET',
			headers,
			body: r.body === undefined ? undefined : JSON.stringify(r.body),
			signal: AbortSignal.timeout(r.timeoutMs ?? 8000),
			redirect: 'error'
		});
	} catch {
		return { ok: false, status: 0, code: 'unreachable', message: 'API unreachable' };
	}
	let payload: unknown = null;
	try {
		payload = await res.json();
	} catch {
		/* empty or non-JSON body */
	}
	if (res.ok) return { ok: true, status: res.status, data: payload as T };
	const e = (payload as { error?: { code?: string; message?: string } } | null)?.error;
	return {
		ok: false,
		status: res.status,
		code: e?.code ?? 'error',
		message: e?.message ?? res.statusText
	};
}
