# Spillway web

SvelteKit 3 (adapter-node). Lihat bagian "Web" di README utama dan keputusan 76-86 di `docs/DECISIONS.md`.

- `npm run dev` / `build` / `preview`; `npm run check`, `lint`, `test` (Vitest), `test:e2e` (Playwright), `size`.
- Teks UI hanya dari `src/lib/messages.ts`. Impor modul TS memakai `#lib/<nama>.ts`. Server-only: `src/lib/server/`.
