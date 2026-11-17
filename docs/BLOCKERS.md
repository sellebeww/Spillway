# Blockers

Tidak ada blocker yang menghentikan Fase 1.

## Risiko diterima

- `npm audit`: `deepmerge` (GHSA-gcqx-w48m-w2h4) transitif dari `@sveltejs/vite-plugin-svelte`, hanya saat build. `npm audit fix --force` menurunkan SvelteKit ke 0.0.30, jadi tidak diterapkan. Tinjau ulang saat dependensi diperbarui.

## Fase 8 (2026-11-17)

- **(Teratasi)** Uji beban dan smoke compose awalnya tertahan karena Docker Desktop tidak stabil; setelah Docker dimulai ulang keduanya dijalankan dan lulus (docs/LOADTEST.md). Penyebab Postgres berhenti sekali ("exit code 2") saat Keycloak start pertama dan beban proyek lain tidak diselidiki dan tidak muncul lagi.
- **Firmware belum diuji di hardware.** Hanya `pio run` (sukses, tanpa peringatan, flash 76,9 persen) dan `pio test -e native` (9 test, golden vector) yang dijalankan.

## Fase 9 (2026-11-17)

Semua butir audit yang dapat dinilai agen PASS (docs/AUDIT.md). Yang tersisa terbukti membutuhkan lingkungan nyata dan tidak dapat diselesaikan agen; langkah uji ada di [ENV_TEST.md](ENV_TEST.md):

- **L1 Bot Telegram nyata** (Wajib 5): tidak ada token bot. Kode, outbox, bot dan pesan diuji dengan server Telegram tiruan. Opsi: pemilik menjalankan ENV_TEST bagian A.
- **L2 ESP32 dan sensor nyata** (Wajib 9): tidak ada perangkat keras. Firmware ter-build dan 9 test host lulus. Opsi: ENV_TEST bagian B.
- **L3 VPS, DNS, TLS publik, MQTT TLS, reboot, backup/restore di server nyata** (Wajib 10): tidak ada VPS dan domain; smoke berjalan lewat HTTP di `localhost`. Opsi: ENV_TEST bagian C.
- **L4 Keycloak produksi** (Wajib 7): hanya realm dev yang diuji di peramban nyata. Opsi: ENV_TEST bagian D.
- **L5 CI GitHub Actions**: belum pernah dijalankan. Satu penyebab kegagalan pasti (`.env.prod.example` tidak terlacak) sudah diperbaiki; sisanya baru terlihat setelah push ke `main`.
- **L6 Tile OpenStreetMap nyata**: hanya tile tiruan di Playwright.
- **L7 Kapasitas VPS dan perilaku jangka panjang**: uji beban hanya di laptop selama menit.

Bukan blocker lingkungan tetapi batas yang diakui: uji aksesibilitas penuh (axe) tidak dijalankan (BACKLOG); hanya penjaga heuristik di `web/e2e/a11y.e2e.ts`.
