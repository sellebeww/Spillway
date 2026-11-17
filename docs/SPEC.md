# Spillway: kontrak proyek

Dokumen ini adalah sumber kebenaran tunggal. Baca penuh sebelum berkontribusi, lalu baca `docs/PROGRESS.md`. Jika instruksi fase bertentangan dengan dokumen ini, dokumen ini menang kecuali instruksi fase menyebut pengecualian secara eksplisit.

## 1. Tujuan proyek

Spillway adalah platform sumber terbuka bagi komunitas (desa, relawan siaga banjir, BPBD kecil, peneliti) untuk menerima data tinggi muka air dari sensor murah, menilai kualitas datanya, mengevaluasi aturan peringatan, dan mengirim peringatan, tanpa operator menyentuh kode.

Alur utama: sensor (ESP32 + ultrasonik, atau simulator) -> MQTT/HTTP -> ingest idempoten -> kalibrasi datum -> skor kualitas -> TimescaleDB -> rule engine (ambang bertingkat + laju kenaikan) -> notifikasi Telegram, serta peta dan grafik langsung (SSE) dengan halaman publik read-only per titik.

Pembeda: dibuat untuk semantik banjir (laju kenaikan, ambang per titik, datum kalibrasi), skor kualitas per sensor untuk menekan alarm palsu, skema siap multi-tenant.

Tujuan portofolio: sistem real-time end-to-end (sensor, broker, time-series DB, alert, UI) dengan cerita beban dan keandalan yang terukur untuk interviewer backend dan data engineering.

### Pernyataan keselamatan (wajib tampil di README, halaman publik, dan pesan Telegram bila relevan)
Spillway adalah alat bantu komunitas dan studi kelayakan. BUKAN sistem peringatan dini resmi dan tidak menggantikan BMKG, BNPB, atau BPBD. Peringatan bisa terlambat atau gagal terkirim. Nilai ambang harus ditetapkan oleh pihak berwenang setempat.

Prinsip fail-visible: data basi tidak boleh tampil seolah terkini. Jika pembacaan terakhir lebih tua dari `2 x interval_harapan` (minimal 5 menit), UI publik dan pesan menampilkan status "DATA TERTUNDA" dan waktu pembaruan terakhir secara mencolok.

## 2. Pemilik dan aturan repo

- Pemilik dan pemegang hak cipta: **Vassel Goleyu**. Cantumkan di `LICENSE` (MIT, "Copyright (c) 2026 Vassel Goleyu"), `go.mod` modul `github.com/sellebeww/spillway`, `package.json`, header singkat di file sumber utama, dan README.
- Commit hanya memuat pesan perubahan; author dan kontributor adalah pemilik repo.
- Jangan memakai tanda em dash (U+2014) di teks apa pun (kode, komentar, dokumen, commit, UI). Pakai koma, titik, titik dua, atau kurung.
- Commit kecil, format Conventional Commits (`feat(ingest): ...`). Minimal satu commit per langkah besar, wajib satu commit di akhir tiap fase.
- Jangan commit secret, `.env`, kredensial, atau artefak build. Sediakan `.env.example` dengan nilai dummy.
- Bahasa: UI dan pesan notifikasi Bahasa Indonesia (via file pesan terpusat, tanpa string hardcoded tersebar). Kode, komentar, identifier, README, commit Bahasa Inggris. Dokumen kerja `docs/*.md` boleh Bahasa Indonesia.

## 3. Scope lock

### Wajib (MVP, semua harus selesai)
1. Registrasi site (lokasi di peta) dan device; kalibrasi offset datum dengan riwayat versi (`valid_from`); operator mengelola semuanya lewat UI admin.
2. Ingest MQTT dan HTTP, idempoten, menangani duplikat, pesan terlambat, dan pesan tidak berurutan.
3. Skor kualitas data dasar: di luar jangkauan, lonjakan (Hampel), sensor macet (flatline), kesegaran, baterai rendah, kemiringan baterai harian.
4. Dua jenis aturan: `THRESHOLD` bertingkat dan `RATE_OF_RISE`, dengan konfirmasi, histeresis, dan cooldown anti-spam.
5. Notifikasi Telegram (berlangganan per site lewat deep link, perintah dasar).
6. Peta dengan grafik langsung (SSE) dan halaman publik read-only per titik.
7. Auth OIDC (Keycloak) dengan peran admin, operator, publik; semua data dibatasi per organisasi.
8. Simulator armada sensor (CLI) dan uji beban terukur.
9. Satu sensor ESP32 + ultrasonik di ember air untuk demo (firmware di repo).
10. Deploy Docker Compose di belakang Caddy dan CI GitHub Actions.

### Dilarang dikerjakan (di luar scope, jangan tambahkan walau terasa berguna)
LoRaWAN dan ChirpStack, SMS, web push, UI onboarding multi-tenant penuh dan billing, integrasi Grafana atau ThingsBoard, aplikasi mobile, prakiraan ML, EMQX, Kubernetes, multi-bahasa selain Indonesia, dashboard Grafana, notifikasi email, aturan jenis ketiga (termasuk alarm sensor mati; status offline hanya tampil di UI). Arsitektur harus siap ke arah itu, fiturnya tidak dibangun.

Jika menemukan ide di luar daftar Wajib, catat satu baris di `docs/BACKLOG.md` dan lanjutkan.

## 4. Keputusan terkunci (jangan diubah, jangan ditanyakan)

| Hal | Keputusan |
|---|---|
| Backend | Go (versi stabil terbaru), satu binary `spillway` dengan subperintah `serve` dan `migrate`; binary terpisah `simulator` |
| Pustaka Go | `chi` (router), `pgx/v5` (+ `pgxpool`), `goose` (migrasi, di-embed), `eclipse/paho.golang` (MQTT v5, autopaho), `go-redis/v9`, `log/slog`, `prometheus/client_golang`, `testcontainers-go` untuk integrasi. Verifikasi koordinat dan versi dari sumber resmi; jangan menebak |
| Broker | Mosquitto (EMQX ditunda) |
| Database | PostgreSQL + TimescaleDB + PostGIS (verifikasi image yang menyediakan ketiganya, atau gabungkan secara terdokumentasi), Redis untuk last-value cache dan pub/sub |
| Frontend | SvelteKit (TypeScript, adapter-node), MapLibre GL, ECharts (impor modular), CSS biasa dengan variabel CSS (tanpa framework UI), Vitest, Playwright untuk smoke test |
| Auth | Keycloak OIDC (code flow + PKCE di sisi SvelteKit server, cookie httpOnly), Go memvalidasi JWT lewat JWKS dengan cache. Realm di-import dari `deploy/keycloak/realm-dev.json` (khusus dev) |
| Real-time | Server-Sent Events, difan-out lewat Redis pub/sub |
| Basemap | URL tile dari env `TILE_URL`; default tile OpenStreetMap hanya untuk demo skala kecil dengan atribusi, dokumentasikan kebijakan penggunaannya |
| Deploy | Docker Compose, Caddy (TLS otomatis), image non-root multi-stage |
| Peran | `admin` (seluruh organisasi), `operator` (site tertentu via memberships), `public` (tanpa login) |
| Waktu | Semua timestamp UTC (`timestamptz`); zona tampilan per site (default `Asia/Jakarta`) |
| Satuan | `raw_value` = jarak sensor ke permukaan air dalam mm; `level_cm` = tinggi muka air terhadap datum |
| Severity | `WASPADA` (1), `SIAGA` (2), `AWAS` (3) |
| Lisensi | MIT |

## 5. Spesifikasi domain (wajib diikuti persis)

### 5.1 Payload dan ingest
- Topik: `spillway/v1/{device_key}/telemetry`. HTTP: `POST /v1/ingest` dengan payload sama.
- Payload JSON: `{"device":"<device_key>","sid":"<id sesi acak per boot>","fc":<uint32 naik per sesi>,"ts":<epoch detik UTC>,"raw_mm":<int>,"batt_v":<float opsional>,"sig":"<hex HMAC-SHA256>"}`.
- `sig = HMAC-SHA256(secret_device, "device|sid|fc|ts|raw_mm|batt_v")` (nilai `batt_v` kosong jika tidak ada; format kanonik tepat didokumentasikan di `docs/PROTOCOL.md` dan diuji dengan golden vector yang juga dipakai firmware). Verifikasi dengan perbandingan waktu-konstan. Gagal verifikasi: tolak, hitung di metrik, jangan simpan.
- Secret device dibuat acak saat registrasi, ditampilkan SATU kali, disimpan terenkripsi AES-GCM dengan kunci master `SPILLWAY_SECRET_KEY` (env).
- Idempotensi: kunci (`device`, `sid`, `fc`). Lapis 1: Redis `SET NX` dengan TTL 24 jam. Lapis 2 (pengaman): unique index `readings(device_id, ts, sid, fc)`; insert dengan `ON CONFLICT DO NOTHING`. Redis mati: fail-open ke lapis 2 (jangan menolak data).
- Selisih jam: jika `|ts - waktu_server| > 10 menit` dan pesan bukan jelas terlambat, tandai `CLOCK_SKEW` (skor 0,5) dan tetap simpan dengan `ts` perangkat.
- Terlambat: `waktu_server - ts` > 2 menit = `late=true`. Diterima sampai 24 jam; lebih tua ditolak dan dihitung di metrik. Data terlambat disimpan dan tampil di grafik, tetapi TIDAK memicu evaluasi aturan kecuali `ts` masih dalam 5 menit dari pembacaan valid terbaru perangkat itu.
- Urutan per perangkat dipertahankan (pemrosesan di-shard berdasarkan hash `device_key`). Pesan lintas-perangkat boleh paralel.
- Kalibrasi memakai versi yang `valid_from <= ts` (waktu pembacaan, bukan waktu tiba).

### 5.2 Kalibrasi
`level_cm = offset_cm - raw_mm / 10`, dengan `offset_cm` = elevasi kepala sensor di atas datum nol (cm). Kalibrasi menyimpan `offset_cm`, `min_valid_mm` (default 250), `max_valid_mm` (default 4500), `valid_from`, `version`, `note`, `created_by`. Default adalah angka awal yang dapat diubah, bukan klaim spesifikasi sensor tertentu. Kalibrasi tidak boleh diubah, hanya ditambah versi baru (append-only, `valid_from` tidak boleh mundur ke sebelum versi aktif terakhir tanpa konfirmasi eksplisit di API).

### 5.3 Kualitas data (dokumentasikan rumus di `docs/QUALITY.md`)
- Flag per pembacaan dan skor titik 0..1; skor titik = minimum dari skor komponen:
  - `OUT_OF_RANGE` (raw di luar `[min_valid_mm, max_valid_mm]`): 0,0
  - `SPIKE` (Hampel kausal: median dan MAD dari 7 nilai valid terakhir, ambang 3 x 1,4826 x MAD dengan lantai MAD 0,5 cm; butuh >= 5 nilai riwayat): 0,2
  - `FLATLINE` (nilai `raw_mm` identik persis pada N=20 pembacaan berurutan): 0,3
  - `CLOCK_SKEW`: 0,5
  - `LOW_BATTERY` (`batt_v` di bawah ambang per perangkat, default 3,3 V): 0,9
  - `LATE`: informasional, 1,0
  - lainnya `OK`: 1,0
- Skor kesehatan perangkat (jendela 1 jam): `rasio_pembacaan_diterima x rata-rata_skor_titik`. Tren baterai: kemiringan `batt_v` per hari dari regresi linear 24 jam (tampil di UI kesehatan perangkat).
- Pembacaan dengan skor < `min_quality` (default 0,5) disimpan tetapi TIDAK dipakai aturan.
- Status perangkat: `ONLINE`, `DELAYED` (> 2 x interval_harapan), `OFFLINE` (> 10 x interval_harapan atau 15 menit, mana yang lebih besar).

### 5.4 Aturan peringatan
- `THRESHOLD`: parameter `tiers=[{level_cm, severity}]`. Buka/naikkan severity jika `level_cm` melewati tingkat pada 2 dari 3 pembacaan valid berurutan terakhir (konfirmasi). Turun/tutup jika berada di bawah tingkat dikurangi `hysteresis_cm` (default 5) pada 3 pembacaan valid berurutan.
- `RATE_OF_RISE`: regresi linear OLS pada pembacaan valid 10 menit terakhir (sumbu x = detik dari `ts`), butuh >= 4 titik dan rentang >= 5 menit; hasil `cm/jam`; parameter `tiers=[{cm_per_hour, severity}]`; konfirmasi 2 evaluasi berurutan; histeresis 20 persen dari tingkat.
- Satu `alert_event` terbuka per aturan; severity berubah pada event yang sama (naik atau turun), tutup saat di bawah tingkat terendah (histeresis).
- Notifikasi: saat OPENED dan saat ESCALATED langsung (melewati cooldown); pengingat severity sama hanya sekali per `cooldown` (default 30 menit); sekali saat de-eskalasi dan sekali saat CLOSED. Pesan memuat site, severity, `level_cm`, laju (jika ada), waktu lokal, tautan halaman publik, dan pernyataan keselamatan singkat.
- State jendela aturan dibangun ulang dari DB saat start (hasil identik dengan state live; diuji).

### 5.5 Telegram
Long polling `getUpdates` (tanpa webhook). Perintah: `/start <token>` (langganan site lewat deep link `https://t.me/<bot>?start=<token>`), `/stop`, `/status`. Pengiriman lewat tabel outbox dengan retry backoff, idempoten (satu notifikasi per `alert_event` + jenis), patuhi 429 `retry_after`, nonaktifkan channel pada 403 (bot diblokir). Token bot via env, tidak pernah di log.

### 5.6 Agregasi dan retensi
Continuous aggregate `readings_1m`, `readings_1h`, `readings_1d` (min, rata-rata, maks `level_cm`, jumlah, hanya pembacaan skor >= `min_quality`). Kompresi chunk > 7 hari, retensi mentah default 90 hari (konfigurasi). API grafik memilih resolusi otomatis dari rentang waktu.

### 5.7 Keamanan dan privasi
Semua query terscope ke organisasi pengguna (uji negatif lintas organisasi wajib). Endpoint publik: rate limit per IP, tidak pernah membocorkan secret, id internal pengguna, atau data site non-publik. Audit log untuk semua perubahan konfigurasi. Batasan jujur dicatat di README (Threat model): kredensial broker bersama, replay dibatasi oleh `fc` dan jendela waktu, tanpa TLS perangkat jika tidak dikonfigurasi, ketersediaan Telegram di luar kendali.

## 6. Aturan kerja

1. **Kerjakan.** Tulis kode dan test, jalankan build, perbaiki.
2. **Ambiguitas** diputuskan sesuai dokumen ini dan dicatat di `docs/DECISIONS.md` (tanggal, masalah, keputusan, alasan).
3. **Satu fase pada satu waktu.** Jangan mengerjakan fase lain lebih awal dan jangan menambah fitur di luar scope.
4. **Test-first untuk logika domain** (kualitas, kalibrasi, aturan, kanonikalisasi). Tulis test gagal dahulu.
5. **Verifikasi nyata.** Perintah standar (sesuaikan nama setelah Fase 1, simpan di Makefile target `make verify`):
   `go vet ./... && golangci-lint run && go test -race ./... && (cd web && npm run check && npm run lint && npm test && npm run build)`
   Tempel ringkasan hasil nyata di laporan akhir fase. Dilarang menulis "lulus" tanpa menjalankannya.
6. **Perbaiki semua temuan** (error, test gagal, race, lint, warning relevan) sebelum menutup fase; jalankan ulang seluruh verifikasi sampai bersih. Jika sebuah masalah gagal diperbaiki setelah 3 percobaan berbeda, tulis akar masalah dan opsi di `docs/BLOCKERS.md`, lanjut ke bagian lain.
7. **Status jujur.** Setiap butir hanya boleh: `SELESAI-TERVERIFIKASI` (ada test atau build yang dijalankan), `SELESAI-PERLU-UJI-LINGKUNGAN` (kode dan test ditulis, butuh Docker, perangkat ESP32, bot Telegram nyata, atau VPS), `BELUM`. Perilaku yang butuh ESP32, token bot Telegram nyata, atau VPS tidak boleh diklaim terbukti sebelum diuji di sana.
8. **Cek Docker lebih dulu.** Jika Docker tersedia, jalankan integrasi nyata. Jika tidak, coba pasang layanan secara native; jika tetap tidak bisa, tulis test integrasi lengkap, tandai "ditulis, belum dijalankan", dan jangan membuat klaim performa.
9. **Angka kinerja hanya dari pengukuran nyata.** Catat spesifikasi mesin, versi, parameter, dan skala yang benar-benar dijalankan. Jangan mengekstrapolasi diam-diam ke 10.000 sensor; sediakan perintah agar pemilik menjalankannya di VPS.
10. **Kualitas kode.** Tanpa kode mati, tanpa `panic` di jalur produksi, error selalu dibungkus dengan konteks, `context.Context` mengalir, tanpa goroutine bocor (uji dengan `goleak` pada paket konkuren), tanpa SQL hasil perakitan string dari input, tanpa log yang memuat secret, token, atau payload mentah berisi sig.
11. **Ringkas.** Laporan memuat ringkasan hasil, bukan log mentah.

## 7. Protokol progres

`docs/PROGRESS.md` adalah catatan kemajuan antar tahap: tabel fase 1-9 dengan status; daftar butir Wajib 1-10 dengan status jujur (aturan 7) dan lokasi kode atau test; "Konteks untuk tahap berikutnya" maksimal 15 baris (apa yang ada, perintah penting, jebakan). Perbarui di akhir setiap fase dan commit bersama kode.

## 8. Definition of Done global

Sebuah fase selesai hanya jika: DoD fase terpenuhi; verifikasi standar bersih (atau kendala lingkungan dicatat jujur di BLOCKERS.md); tidak ada perubahan di luar scope lock; `PROGRESS.md` dan `DECISIONS.md` diperbarui dan commit dibuat sesuai aturan repo; laporan akhir fase (maks 25 baris): apa yang dibuat, hasil verifikasi nyata, status per butir, masalah terbuka.
