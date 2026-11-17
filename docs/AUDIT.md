# Audit independen (Fase 9)

Tanggal: 2026-11-17. Metode: audit yang tidak mempercayai klaim di `docs/PROGRESS.md`; setiap butir dibuktikan ulang dengan perintah, pembacaan kode, atau percobaan nyata. Mesin: macOS arm64 (Apple M1, 8 GB), Docker Desktop 29.8.1, Go 1.27.4, Node 25.

Kolom **Awal** adalah status sebelum perbaikan Fase 9, **Akhir** sesudahnya. FAIL berarti tidak ada bukti, atau bukti menunjukkan perilaku yang salah. Butir yang hanya dapat dibuktikan di lingkungan nyata ada di bagian L dan tidak dihitung sebagai PASS atau FAIL.

**Ringkasan hitungan** (73 butir yang dapat dinilai PASS atau FAIL):

| | PASS | FAIL |
|---|---|---|
| Awal (sebelum perbaikan Fase 9) | 55 | 18 |
| Akhir | 73 | 0 |

FAIL awal yang ditemukan dan diperbaiki (18): T-W10, T-53, S4, S5, K7, K11, H4, H7, F1, F2, F3, F4, U2, C1, C2, C3, C4, C7. Tujuh butir lingkungan nyata (L1 sampai L7) tidak dihitung.

## A. Bukti dasar (Tahap A)

| ID | Butir | Awal | Akhir | Bukti |
|---|---|---|---|---|
| A1 | `make verify` penuh dari kondisi bersih (`go clean -cache -testcache`, hapus `web/build`, `web/.svelte-kit`) | PASS | PASS | Awal: exit 0 (go vet, golangci-lint 0 issues, `go test -race` semua paket ok, svelte-check 0 galat 0 peringatan, prettier+eslint bersih, 132 test Vitest, build). Akhir: lihat "Hasil akhir" di bawah |
| A2 | `go test -race -count=1 ./...` | PASS | PASS | Awal: exit 0, 15 paket ok. Akhir: exit 0 (11:54, 15 paket ok, termasuk penjaga goleak baru) |
| A3 | Test integrasi dengan Docker nyata | PASS | PASS | Tidak ada build tag: integrasi (Timescale, Redis, Mosquitto lewat testcontainers) ikut A1 dan A2; paket `ingest` 98 dtk, `store` 75 dtk, `api` 86 dtk, `alerts` 76 dtk, `notify` 78 dtk |
| A4 | Build image (`spillway` 48 MB, `spillway-web` 384 MB, `spillway-keycloak` 694 MB) | PASS | PASS | `docker compose ... up --build` selesai di smoke awal dan akhir |
| A5 | Smoke compose produksi (`deploy/scripts/smoke.sh`) | PASS | PASS | Awal: `SMOKE OK`, 1m30s. Akhir, dari volume kosong, termasuk langkah 5b baru (restart broker dan Postgres): `SMOKE OK`, 3 menit. Port 8080 host dipakai proyek lain, jadi dipakai `SPILLWAY_LOCAL_PORT=18080` |
| A6 | Playwright e2e (`make web-e2e`) | PASS | PASS | 4 test awal lulus; akhir 8 test (4 baru penjaga a11y) lulus |

## T. Traceability: butir Wajib dan aturan docs/SPEC.md ke kode produksi dan test

Nama test diverifikasi ada: 118 dari 119 nama di PROGRESS.md ada sebagai fungsi test (yang ke-119 adalah wildcard `TestLimiter*`, ada di `internal/ingest/limiter_test.go`).

| ID | Butir | Awal | Akhir | Kode produksi | Test penguji |
|---|---|---|---|---|---|
| T-W1 | Registrasi site/device, kalibrasi versi, UI admin | PASS | PASS | `internal/api/admin.go`, `internal/store/{sites,devices}.go`, `internal/calibration`, `web/src/routes/admin/*` | `TestCalibrationSelectionAndAppendOnly` (`store/integration_test.go`), `TestSecretIsShownExactlyOnce` (`api/api_test.go:401`), `calibration.TestResolve`, `TestValidateAppend`, Playwright form kalibrasi |
| T-W2 | Ingest MQTT dan HTTP idempoten, duplikat, terlambat, tidak berurutan | PASS | PASS | `internal/ingest/{pipeline,shard,mqtt,http,redis}.go` | `TestIngestMQTTExactResults` (`integration_test.go:281`), `TestIngestSurvivesRedisOutage` (:375), `TestIngestRestartMidStreamLosesNothing` (:438), `TestPerDeviceOrderIsPreservedUnderConcurrency` |
| T-W3 | Skor kualitas | PASS | PASS | `internal/quality/{quality,health}.go`, dipanggil di `ingest/shard.go` | 19 test di `quality_test.go`, `TestRebuildMatchesLive` (:276) |
| T-W4 | THRESHOLD dan RATE_OF_RISE | PASS | PASS | `internal/rules/engine.go`, `internal/alerts/sink.go` | `rules/engine_test.go`, `alerts.TestFloodWaveGivesTheFullAlertLifecycle`, baru: `simulate.TestFloodWaveThresholdMatchesHandCalculation` dan `...RateOfRise...` |
| T-W5 | Telegram | PASS | PASS | `internal/notify/{bot,sender,telegram,messages}.go`, `store/{channels,alertflow}.go` | 12 test fungsi di `notify_test.go` (banyak subtes) (server Telegram tiruan); bot nyata: lihat L1 |
| T-W6 | Peta, SSE, halaman publik | PASS | PASS | `internal/api/{public,hub}.go`, `web/src/routes/{+page,s/[slug]}` | `hub_test.go` (4 test, goleak), `public_test.go`, Playwright (peta, DATA TERTUNDA tanpa JavaScript) |
| T-W7 | Auth OIDC, peran, isolasi org | PASS | PASS | `internal/auth/jwt.go`, `internal/api/server.go`, `web/src/lib/server/oidc.ts` | `auth.TestVerifyRejectsEverythingElse`, `TestRolesAdminOperatorAndCrossSite`, `TestCrossOrganizationIsolation`, `oidc.spec`, `session.spec` |
| T-W8 | Simulator dan uji beban | PASS | PASS | `internal/simulate`, `cmd/simulator`, `tools/loadtest` | `simulate_test.go` (6 skenario), `tools/loadtest/main_test.go`, hasil mentah `docs/loadtest-results/*.json` (angka di LOADTEST.md cocok dengan JSON: 260.000 dan 250.000 baris, `lost = 0`) |
| T-W9 | Firmware ESP32 | PASS | PASS | `firmware/esp32/{src,lib/spillway_core}` | `pio test -e native` 9 test dengan golden vector (`test_core.cpp:153-161`); perangkat keras: lihat L2 |
| T-W10 | Deploy Compose + Caddy + CI | FAIL | PASS | `deploy/compose/docker-compose.prod.yml`, `deploy/caddy/Caddyfile`, `.github/workflows/ci.yml` | Awal: job CI `smoke` memakai `deploy/compose/.env.prod.example` yang TIDAK ada di git (`.gitignore` baris `.env.*` menyembunyikannya), jadi job itu pasti gagal dan panduan DEPLOY tidak bisa diikuti dari klon baru. Akhir: negasi `.gitignore`, berkas dilacak, `TestProdEnvExampleIsTrackedAndComplete`, smoke lokal lulus. Job CI sendiri belum pernah dijalankan di GitHub (L5) |
| T-51 | 5.1 payload, HMAC waktu-konstan, idempotensi, skew, late, urutan, kalibrasi menurut waktu baca | PASS | PASS | `protocol/protocol.go:139`, `ingest/{pipeline,shard,redis}.go`, `quality.go` | `TestGoldenVectors`, `FuzzParse`, `TestClockSkewAndLate`, `TestLateDataPolicy`, `TestRedisLayerAndFailOpen`. Deviasi SET NX terdokumentasi (DECISIONS 42), dokumen PROTOCOL.md salah: lihat C4 |
| T-52 | 5.2 rumus level, append-only, `valid_from` tidak mundur | PASS | PASS | `internal/calibration/calibration.go`, `store/devices.go` | `TestLevel`, `TestResolve`, `TestValidateAppend`, `TestCalibrationSelectionAndAppendOnly` |
| T-53 | 5.3 kualitas (flag, skor minimum, kesehatan, status) | FAIL | PASS | `internal/quality` | Awal: klaim desain "pergeseran level menetap diterima setelah jendela beradaptasi" (QUALITY.md) tidak punya test. Akhir: `TestPersistentLevelShiftIsAcceptedOnceTheWindowAdapts` (4 ditandai, ke-5 diterima, sesuai prediksi analisis tangan) |
| T-54 | 5.4 aturan, konfirmasi, histeresis, cooldown, rebuild | PASS | PASS | `rules/engine.go`, `alerts/sink.go` | Catatan: oracle test lama (`alerts_test.go` `oracle()`) adalah engine yang sama, bukan independen. Ditambah oracle tangan, lihat D1. `TestRebuildMatchesLive` (`rules/engine_test.go:452`), `TestRestartRebuildsTheSameStateAsLive` |
| T-55 | 5.5 Telegram: long polling, outbox idempoten, 429, 403, token tak di log | PASS | PASS | `notify/*`, `store/channels.go` | `TestSenderDeliversEachNotificationExactlyOnce` (:398), `TestSenderRetryAfterIsRespected`, `TestSenderDeactivatesBlockedChannelsAndRetriesRateLimits`, `TestClientErrorsAndTokenScrubbing` |
| T-56 | 5.6 agregasi, kompresi, retensi, resolusi otomatis | PASS | PASS | `migrations/00003_readings.sql`, `00004_aggregates.sql`, `store/readings.go:112` | `TestContinuousAggregatesMatchManualCalculation`, `TestRetentionIsConfigurable`, `TestMigrationsUpAndDown`, `TestPublicSeriesPicksResolutionAndFiltersLowQuality` |
| T-57 | 5.7 keamanan, privasi, audit log, batasan jujur di README | PASS | PASS | `store/scope.go`, `api/*` | `TestOrganizationIsolation`, `TestCrossOrganizationIsolation`, `TestEveryMutationIsAudited`, `TestAuditLog`; Threat model di README |

## S. Scope

| ID | Butir | Awal | Akhir | Bukti |
|---|---|---|---|---|
| S1 | Tidak ada fitur terlarang (LoRaWAN, SMS, push, Grafana, EMQX, k8s, i18n, e-mail, jenis aturan ke-3) | PASS | PASS | `git grep -iE 'lorawan\|chirpstack\|twilio\|sms\|webpush\|grafana\|emqx\|kubernetes\|smtp\|stripe\|i18n'` di luar docs: 0 hasil |
| S2 | Dependensi | PASS | PASS | Go langsung (`go.mod`): daftar terkunci (chi, pgx, goose, paho.golang, go-redis, prometheus, testcontainers) ditambah `google/uuid`, `gopkg.in/yaml.v3` (test kontrak OpenAPI), `go.uber.org/goleak` (aturan 6.10): ketiganya kecil dan dibenarkan. Web (`package.json`): hanya `echarts` dan `maplibre-gl` sebagai dependensi runtime |
| S3 | Endpoint dan halaman semua terpetakan ke Wajib | PASS | PASS | `api/server.go:222-259`: 4 rute publik, 22 rute terautentikasi, `/v1/ingest`, `/healthz`, `/readyz`, `/metrics`; halaman: peta, `/s/[slug]`, 8 halaman admin (sites, devices, calibration, rules, telegram, alerts, audit, beranda), 3 rute auth |
| S4 | Tidak ada placeholder mati | FAIL | PASS | Awal: `deploy/caddy/.gitkeep` (folder berisi Caddyfile) dan `internal/realtime/.gitkeep` (paket kosong, SSE ada di `api`). Dihapus |
| S5 | Header pemilik di sumber utama (docs/SPEC.md 2) | FAIL | PASS | Awal: 9 berkas non-test tanpa header (5 skrip shell, `hmac_mbedtls.h`, `app.d.ts`, `playwright.config.ts`, e2e). Ditambah. Berkas test dan komponen Svelte tidak diberi header (bukan "sumber utama"; repo konsisten begitu) |
| S6 | Tanpa em dash | PASS | PASS | `git grep -nI $'\xe2\x80\x94'`: 0 hasil |
| S7 | Riwayat git bersih | PASS | PASS | `git log` 31 commit: satu author (Vassel Goleyu), tanpa trailer `Co-Authored-By` |

## K. Keamanan

| ID | Butir | Awal | Akhir | Bukti |
|---|---|---|---|---|
| K1 | Isolasi org di SETIAP query | PASS | PASS | `store/scope.go:45-72`: query tenant hanya lewat `OrgStore.exec/query` yang menolak SQL tanpa `org_id = $1` (`ErrUnscoped`). Grep akses pool langsung: 17 query, semua bertanda `-- unscoped:` dan terdaftar di `TestNoUnscopedDatabaseCalls` (`unit_test.go:68`, AST). Saya baca ulang yang lintas-org: `PublicSites` (`apiqueries.go:115`) hanya `is_public`, tanpa secret atau id pengguna; `DeviceAuthByKey` (`devices.go:114`); `RedeemSubscriptionToken` (token 192 bit, hash); sisanya pekerja global |
| K2 | Uji negatif lintas organisasi | PASS | PASS | `TestCrossOrganizationIsolation` (`api_test.go:266`): admin org B terhadap 18 endpoint org A, semua 404, tanpa kebocoran, data A tak berubah. Dari 22 rute terautentikasi, 4 sisanya (`/me`, `GET /sites`, `POST /sites`, `/audit`) hanya mengembalikan atau membuat data milik pemanggil (kebocoran daftar dan audit diperiksa di :302-307). `TestOrganizationIsolation` (`store/isolation_test.go:17`) untuk setiap repository |
| K3 | Secret tak pernah di log atau respons ulang | PASS | PASS | Pemindaian nyata: 20 secret perangkat dan 3 kredensial infrastruktur (sandi MQTT, Postgres, kunci master) dicari di log 7 kontainer stack (spillway, web, caddy, mosquitto, postgres, redis, keycloak): 0 kecocokan. `TestSecretIsShownExactlyOnce`, `telegram.go:65` (scrub token), `config.LogValue` |
| K4 | HMAC waktu-konstan | PASS | PASS | `ingest/protocol/protocol.go:139` `hmac.Equal`; satu-satunya `==` pada tanda tangan adalah dedupe keputusan internal (`alerts/sink.go:340`), bukan otentikasi |
| K5 | SQL terparameter | PASS | PASS | `grep` perakitan SQL (`Sprintf`, `+` dengan kata kunci SQL): 0 hasil; `Sprintf` hanya untuk nilai di `seedload.go`; retensi memakai `make_interval(days => $1)` (`readings.go:121`) |
| K6 | Rate limit publik | PASS | PASS | `server.go:72` publik 5/dtk burst 30 per IP; `httputil.go:123` IP klien dari entri TERAKHIR `X-Forwarded-For` hanya bila `TRUST_PROXY_HEADERS`; SSR meneruskan IP pengunjung (`web/src/lib/server/api.ts:23`); `TestPublicEndpointsAreRateLimitedPerIP` (`public_test.go:235`) |
| K7 | Header keamanan | FAIL | PASS | Awal: tidak ada `Strict-Transport-Security` di mana pun (grep). Akhir: Caddy mengirim HSTS untuk dua host; `curl -sI http://localhost/` lewat Caddy: `Strict-Transport-Security: max-age=31536000`, CSP, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy`, `Permissions-Policy`, `Cross-Origin-Opener-Policy`. Host `auth.` sempat memunculkan dua header HSTS (Keycloak menambah sendiri `includeSubDomains`); ditimpa dengan `header >`, kini tepat satu. `TestCaddyfileSendsHSTSOnEverySite` |
| K8 | Penyimpanan token di web | PASS | PASS | `web/src/lib/server/auth.ts:24-26` cookie `httpOnly`, `secure`, `sameSite: lax`; sesi di memori server (`session.ts:25`), peramban hanya memegang id acak (DECISIONS 77); PKCE dan open redirect diuji di `oidc.spec`, `session.spec` |
| K9 | CORS | PASS | PASS | `httputil.go:169` allowlist asal, tanpa kredensial, `*` hanya untuk endpoint publik; `TestCORSAndSecurityHeaders` |
| K10 | Kunci master dan rotasi | PASS | PASS | `auth/secret.go:36` (versi kunci, kunci lama untuk dekripsi), `TestKeyRotation` (`secret_test.go:80`), `TestDeviceSecretIsEncryptedAndBound` (`store/integration_test.go:389`) |
| K11 | Kunci master contoh tidak dipakai di produksi | FAIL | PASS | Awal: `config.Load` menerima kunci `.env.example` (publik) di `SPILLWAY_ENV=production`. Akhir: ditolak tanpa mengulang nilainya, `TestProductionRefusesThePublishedDevelopmentKey`; `.env.prod.example` bebas kunci itu (`deploy_test.go`) |
| K12 | Tanpa secret di riwayat git | PASS | PASS | `git log -p --all -G'(AKIA...\|PRIVATE KEY\|token bot\|xox\|ghp_\|sk-)'` atas 31 commit: 0 hasil; `.env`, `secrets.h`, `deploy/compose/.env.prod` tidak terlacak (`git check-ignore`) |
| K13 | Kredensial dan realm dev tidak ada di stack produksi | PASS | PASS | `TestProdStackCarriesNoDevelopmentCredentials` hijau pada jalan pertama; `realm-prod.json`: 0 pengguna, klien `spillway-web` publik dengan PKCE S256, direct grant mati, brute force aktif, registrasi mati, klaim `org` hardcoded (`oidc-hardcoded-claim-mapper`) |
| K14 | Eksposur jaringan produksi | PASS | PASS | Compose: hanya Caddy 80/443 dan MQTT TLS 8883 terbuka; 1883, API, Keycloak di loopback. Lewat Caddy nyata: `/metrics` 404, `/readyz` 404, `/admin/` di host `auth.` 404 |

## H. Keandalan

| ID | Butir | Awal | Akhir | Bukti |
|---|---|---|---|---|
| H1 | Idempotensi: duplikat, restart, Redis mati | PASS | PASS | `TestIngestMQTTExactResults` (10.800 baris tepat, 0 duplikat), `TestIngestSurvivesRedisOutage`, `TestIngestRestartMidStreamLosesNothing`; chaos nyata: 38 duplikat disuntik, 960 kunci unik di DB |
| H2 | Backpressure | PASS | PASS | `TestBackpressureNeverDropsSilently` (`pipeline_test.go:320`), `TestHTTPBusyAnswers503WithRetryAfter`; 10.000 perangkat: `backpressure_waits = 0`, antrean maks 507 |
| H3 | Shutdown anggun | PASS | PASS | `cmd/spillway/main.go:90-204` urutan: hentikan HTTP, hentikan intake MQTT, kuras pipeline, tutup sink, baru putus broker; `TestCloseDrainsEverythingWithoutLoss`, `TestHardShutdownLeavesUnfinishedMessagesFailed`; chaos `kill -9`: 0 hilang |
| H4 | Goroutine bocor | FAIL | PASS | Awal: `goleak` hanya di `ingest` dan `api/hub_test.go`; `alerts` (shard sink, ticker) dan `notify` (bot, worker pengirim) konkuren tanpa penjaga. Akhir: snapshot `goleak.IgnoreCurrent()` dan verifikasi di `t.Cleanup` pada `newEnv` (`alerts_test.go`) dan `newTenant` (`notify_test.go`); semua test lulus; probe goroutine sengaja bocor membuat penjaga MERAH (`found unexpected goroutines`), lalu dihapus |
| H5 | Pool DB | PASS | PASS | `store/store.go:27-29` `MaxConns = max(16, 2*CPU)` kecuali URL menetapkan `pool_max_conns`; `TestTransientDatabaseErrorsAreRetriedWithoutLoss` |
| H6 | DB mati lalu pulih | PASS | PASS | Chaos nyata, Postgres dimatikan 20 dtk di tengah aliran 20 perangkat: 960 pesan unik di-ACK broker, 960 baris, 960 kunci unik, 0 duplikat; `/readyz` kembali `ready`; 0 baris log WARN/ERROR |
| H7 | Broker mati lalu pulih | FAIL | PASS | Awal (percobaan nyata): setelah `docker stop` lalu `start`, `spillway-mosquitto-1` masuk `Restarting (1)` selamanya, `/readyz` 503: `entrypoint.prod.sh` memakai `mosquitto_passwd -c` yang gagal bila berkas sandi sudah ada ("Unable to open file /mosquitto/passwd for writing. File exists"), padahal kontainer yang direstart mempertahankan filesystem-nya (reboot host, OOM). Akibat: ingest mati permanen. Smoke lama tidak pernah merestart broker. Akhir: `rm -f` sebelum membuat berkas, `docker restart` nyata: healthy, `/readyz` `ready`; smoke langkah 5b merestart broker dan Postgres lalu mengirim satu pesan (lulus dari volume kosong); `TestBrokerEntrypointSurvivesARestart` |
| H8 | Outbox tak menggandakan atau menghilangkan | PASS | PASS | `TestOutboxIsIdempotentAndLeased` (`store/integration_test.go:611`), `TestConcurrentWorkersNeverShareAClaim`, `notify.TestSenderDeliversEachNotificationExactlyOnce` (:398, 4 worker); batas yang diakui: jendela satu permintaan antara "terkirim" dan "ditandai SENT" dapat menggandakan satu pesan (TELEGRAM.md) |

### Percobaan kekacauan (H1, H3, H6, H7)

Stack produksi nyata (`docker-compose.prod.yml`), 20 perangkat simulasi via MQTT QoS 1 selama 100 dtk, 2 persen duplikat (seed 7). t=15 dtk Postgres `docker stop`, t=35 `start`; t=50 Mosquitto `stop`, t=58 `start`; t=65 `docker kill` proses `spillway` lalu `start`. Hasil simulator: `unique` 2000, `unique_confirmed` 960 (selebihnya gagal terkirim saat broker mati, tercatat sebagai galat 1058, bukan hilang), `duplicates_sent` 38. Hasil DB setelah pengurasan: **960 baris, 960 kunci `(device_id, ts, sid, fc)` unik**. Satu-satunya masalah: broker tidak pulih (H7, sudah diperbaiki).

## D. Domain

### D1. Skenario banjir lengkap dihitung tangan, terpisah dari test yang ada

Program terpisah (Python, ditulis dari teks docs/SPEC.md 5.4, tanpa membaca `internal/rules`), skenario `flood-wave` perangkat 0, 300 pembacaan 1 per menit, dasar 60 cm, puncak 200 cm, tier THRESHOLD 100/150/190 cm (WASPADA/SIAGA/AWAS), hysteresis 5 cm, cooldown 30 menit:

| Besaran | Hitung tangan | Sistem |
|---|---|---|
| Level: menit 0 / 100 / 150 / 174 (puncak) / 204 / 240 / 299 | 60,000 / 118,758 / 199,895 / 200,397 / 185,081 / 129,602 / 59,725 cm | `simulate.Level` identik (fungsi yang sama dipakai simulator) |
| THRESHOLD, menit dan jenis | 93 OPENED, 115 ESCALATED, 137 ESCALATED, 167 REMINDER, 197 REMINDER, 207 DEESCALATED, 234 DEESCALATED, 263 CLOSED | **Sama persis** (`TestFloodWaveThresholdMatchesHandCalculation`) |
| Jumlah notifikasi per channel | 8 (1 OPENED, 2 ESCALATED, 2 REMINDER, 2 DEESCALATED, 1 CLOSED) | 8; untuk 2 channel 16 baris outbox (`TestFloodWaveGivesTheFullAlertLifecycle`) |
| Dengan pembulatan 0,1 cm (presisi yang tersimpan) | 93, 115, 136, 166, 196, 207, 234, 263 (tiga tick bergeser satu menit) | Sama persis (varian `quantise` pada test yang sama) |
| Laju OLS maksimum (jendela 10 menit, minimal 4 titik dan rentang 5 menit) | 141,48 cm/jam pada menit 117; 138,15 cm/jam pada menit 100; 26,97 cm/jam pada menit 150 | Tier RATE_OF_RISE 30/60/120 dikonfirmasi pada menit 73, 77, 95 (lihat D4) |

Kedua cara perhitungan interpretasi yang ambigu (cooldown dihitung dari notifikasi terakhir atau dari pengingat terakhir) menghasilkan urutan yang sama. Catatan metodologi: test lama memakai engine yang sama sebagai oracle (`oracle()` di `alerts_test.go`), jadi tidak dapat menangkap penyimpangan dari spesifikasi; angka tangan di atas kini ditanam di `internal/simulate/handcalc_test.go`.

### Butir domain lain

| ID | Butir | Awal | Akhir | Bukti |
|---|---|---|---|---|
| D1 | Skenario banjir lengkap: level, laju, urutan alert, jumlah notifikasi | PASS | PASS | Tabel di atas, sama persis. Dua test penjaga baru |
| D2 | Rumus kualitas terhadap `docs/QUALITY.md` | PASS | PASS | Konstanta `quality.go:19-33` sama dengan tabel; contoh "ambang 8,9 cm" dihitung ulang (jendela 100..106 cm, median 103, MAD 2, 3 x 1,4826 x 2 = 8,9). Deviasi yang terdokumentasi: jendela Hampel ikut memuat nilai yang ditandai spike (alasan di QUALITY.md); konsekuensinya sekarang teruji (T-53) dan tertulis sebagai batas |
| D3 | "Spike dan flatline tidak menghasilkan alarm palsu", dengan angka | PASS | PASS | Hitung tangan (`spike-noise`, 300 pembacaan): 36 spike (2 per 17 menit), 36 ditandai SPIKE, 0 terlewat, 0 pembacaan normal salah tandai. Tanpa gating data yang sama membuka 18 siklus alert palsu (OPENED di menit 6, 23, 40, ...); dengan gating: 0. Gelombang banjir asli melalui Hampel: 0 salah tandai. Sistem: `TestQualityScoringKeepsNoiseFromRaisingAlerts` (SPIKE flag = jumlah spike, 0 event, 0 notifikasi, kontrol tanpa gating > 0). Flatline di level normal: 0 alert. Batas jujur (sensor macet di nilai TINGGI): 19 pembacaan pertama belum ditandai sehingga SATU alert dapat terbuka; setelah itu 0 pengingat (hitung tangan: OPENED di pembacaan ke-20; sistem: 1 event, 1 notifikasi, 0 pengingat) |
| D4 | RATE_OF_RISE: OLS, minimal 4 titik dan 5 menit, 2 evaluasi, histeresis 20 persen | PASS | PASS | Seluruh urutan cocok dengan hitung tangan: 73 OPENED, 77 ESCALATED, 95 ESCALATED, 125 REMINDER (95 + cooldown 30), 138 DEESCALATED, 147 DEESCALATED, 152 CLOSED (`TestFloodWaveRateOfRiseMatchesHandCalculation`). Spesifikasi diam soal jumlah evaluasi untuk turun; sistem dan hitung tangan sama-sama memakai 2 berurutan |

## F. Fail-visible (data basi tidak tampil sebagai terkini)

| ID | Butir | Awal | Akhir | Bukti |
|---|---|---|---|---|
| F1 | UI publik (API) | FAIL | PASS | Awal: `api/public.go:88` menilai basi dari waktu TIBA saja. Pembacaan berumur 10 menit yang baru tiba (skenario `late-burst`, buffer) tampil segar dan ONLINE. Akhir: `quality.IsStaleReading` memakai yang lebih tua antara `ts` dan waktu tiba; `TestIsStaleReadingJudgesTheOlderOfMeasurementAndArrival`, `TestDeviceStateIsStaleWhenTheMeasurementIsOld` |
| F2 | Aliran langsung (SSE) dan klien | FAIL | PASS | Awal: `liveAt = Date.now()` pada setiap event non-late, jadi pembacaan dengan `ts` tua tetapi tidak berstatus late (jam perangkat salah, `CLOCK_SKEW` skor 0,5 tetap tampil) membatalkan label DATA TERTUNDA. Akhir: `isFreshReading(ts, now)` di peta dan halaman site; `stale.spec.ts` (3 test baru). Yang sudah benar: server hanya menyiarkan skor >= 0,5 (`hub.go:163`), event late diabaikan, server tak terjangkau 5 menit memunculkan label sendiri (`deriveFreshness`) |
| F3 | Pesan Telegram `/status` | FAIL | PASS | Awal: `notify/bot.go:216` memakai waktu tiba. Akhir: `IsStaleReading`; `TestBotSubscribeStatusStopFlow` tetap lulus, "belum ada data" dan data lama tetap DATA TERTUNDA |
| F4 | Pesan alert Telegram yang tertahan di outbox | FAIL | PASS | Awal: pesan yang terkirim berjam-jam setelah pembacaan (Telegram mati, 429) terbaca sebagai laporan langsung; hanya baris "Waktu pembacaan" yang kecil. Akhir: `notify.DelayNote` menambah baris tebal DATA TERTUNDA dan selisih menit bila > 5 menit; `TestDelayNoteMarksLateDeliveries` |
| F5 | Peta tanpa server | PASS | PASS | `web/src/lib/stale.ts` `deriveFreshness`, `stale.spec.ts` (10 test); Playwright: situs basi menampilkan banner tanpa JavaScript |

## U. UI

| ID | Butir | Awal | Akhir | Bukti |
|---|---|---|---|---|
| U1 | Tanpa string hardcoded tersebar | PASS | PASS | Pemindaian teks di `*.svelte` di luar `messages.ts`: nol teks UI; satu pengecualian disengaja, singkatan zona `WIB/WITA/WIT` pada `<datalist>` editor site (`admin/sites/[id]/+page.svelte:213-217`, nama zona resmi, bukan kalimat) |
| U2 | Bahasa dokumen | FAIL | PASS | Awal: `web/src/app.html` `<html lang="en">` untuk UI berbahasa Indonesia (pembaca layar memakai suara Inggris). Akhir: `lang="id"`; `messages.spec.ts` dan penjaga a11y |
| U3 | Aksesibilitas | PASS | PASS | Pemeriksaan statis: `focus-visible` dan `prefers-reduced-motion` di `styles.css`, 30 live region (`aria-live`, `role=alert/status`), label pada semua kontrol. Penjaga baru `web/e2e/a11y.e2e.ts` pada DOM nyata (7 halaman termasuk admin): satu `<main>`, satu `<h1>`, heading berurutan, nama aksesibel untuk input, select, tombol, tautan, radio; `lang`. **Batas**: bukan axe penuh dan kontras diperiksa manual (BACKLOG) |
| U4 | State kosong dan galat | PASS | PASS | `messages.ts`: `empty`/`emptyHint` untuk peta, site, perangkat, kalibrasi, aturan, alert, audit; `loadError` per halaman; `+error.svelte` untuk 404 dan galat umum; `describeFailure` di semua `load` admin |
| U5 | Ukuran bundle | PASS | PASS | `npm run size` (gzip, JS+CSS awal): dasar 53,3 kB; peta `/` 60,5 kB; `/s/[slug]` 65,5 kB; admin 54,7 sampai 64,3 kB. Lazy: MapLibre 289,1 kB (+ CSS 9,9 kB; hanya peta dan editor site) dan ECharts 173,9 kB (hanya halaman site). Vite memperingatkan dua chunk > 500 kB (kedua pustaka itu, dimuat dinamis) |

## C. Kejujuran klaim

| ID | Butir | Awal | Akhir | Bukti |
|---|---|---|---|---|
| C1 | README | FAIL | PASS | Awal: "Status: Phase 1 (foundation) ... No business features yet", baris `serve` ganda, campuran bahasa Indonesia, threat model satu kalimat. Akhir: ditulis ulang (Fase 9 bagian D) |
| C2 | Klaim kapasitas VPS di DEPLOY.md | FAIL | PASS | Awal: "VPS 2 vCPU, 4 GB RAM ... cukup untuk ratusan sensor", tidak pernah diukur di VPS. Akhir: ditulis "BELUM diukur di VPS" dengan satu-satunya pengukuran (laptop) |
| C3 | "setara 100.000 perangkat" di LOADTEST.md | FAIL | PASS | Awal: kalimat mengesankan uji 100.000 perangkat. Akhir: dijelaskan setara hanya dalam laju pesan; memori state per perangkat tidak diukur |
| C4 | PROTOCOL.md bagian idempotensi | FAIL | PASS | Awal: menulis `SET NX` Redis, bertentangan dengan kode (`redis.go`: tandai setelah commit) dan DECISIONS 42. Akhir: tiga lapis dan alasannya |
| C5 | Tidak menyiratkan sistem peringatan dini resmi | PASS | PASS | Pernyataan keselamatan ada di README (kutipan atas), `messages.ts:8` (UI) dan `notify/messages.go:21` (setiap pesan Telegram); diuji `TestRenderAlertContent` dan `messages.spec.ts` |
| C6 | Klaim performa hanya dari pengukuran | PASS | PASS | Angka LOADTEST.md dicocokkan dengan JSON mentah (`unique_messages_acknowledged_by_broker`, `rows_in_db_for_load_org`, `lost = 0`, ukuran DB); batas (laptop, generator berbagi CPU, p99 di tepi bucket, pemulihan 1.000 perangkat p99 3,7 dtk tidak diselidiki) tertulis |
| C7 | PROGRESS.md jujur | FAIL | PASS | Awal: Fase 9 "Belum dimulai"; tabel Fase 2 masih memuat "BELUM" yang sudah dikerjakan fase lain (mis. "isi pesan notifikasi", Redis, shard); butir 5 dan 10 tanpa catatan temuan. Akhir: diperbarui, rujukan ke audit ini |

## L. Butir yang membutuhkan lingkungan nyata (tidak dihitung PASS atau FAIL)

Langkah uji lengkap ada di [ENV_TEST.md](ENV_TEST.md); alasan terkendala di [BLOCKERS.md](BLOCKERS.md).

| ID | Butir | Mengapa tidak dapat dibuktikan agen |
|---|---|---|
| L1 | Bot Telegram nyata (Wajib 5) | Tidak ada token bot; hanya server Telegram tiruan |
| L2 | ESP32 dan sensor ultrasonik nyata di ember (Wajib 9) | Tidak ada perangkat keras; hanya `pio run` dan 9 test host |
| L3 | VPS, DNS, TLS publik, MQTT TLS, reboot server, backup/restore di server nyata (Wajib 10) | Tidak ada VPS dan domain; smoke berjalan lewat HTTP di `localhost` |
| L4 | Keycloak produksi (realm-prod, pengguna, klaim `org`, HTTPS) (Wajib 7) | Hanya realm dev yang diuji di peramban nyata; realm prod hanya dianalisis dan lulus impor di smoke |
| L5 | CI GitHub Actions | Belum pernah dijalankan; satu penyebab kegagalan pasti sudah ditemukan dan diperbaiki (T-W10), sisanya hanya terlihat saat dijalankan |
| L6 | Tile OpenStreetMap nyata | Hanya tile tiruan; kebijakan penggunaan OSM tercatat |
| L7 | Kapasitas dan perilaku jangka panjang di VPS target | Uji beban hanya di laptop, menit bukan jam, tanpa kompresi chunk |

## Hasil akhir (rerun Fase 9 setelah semua perbaikan)

Semua dijalankan pada pohon kerja yang sama dengan commit akhir.

| Pemeriksaan | Hasil |
|---|---|
| `go clean -testcache` lalu `make verify` | exit 0: `go vet` bersih; `golangci-lint` 0 issues; `go test -race` 15 paket ok; svelte-check 610 berkas, 0 galat, 0 peringatan; prettier dan eslint bersih; 136 test Vitest di 10 berkas lulus; build web sukses |
| `go test -race -count=1 ./...` | exit 0, 15 paket ok (dijalankan sebelum dua perubahan kecil berikutnya) |
| Sesudah perubahan terakhir (Caddyfile `header >`, regex test deploy, urutan RATE_OF_RISE penuh di `handcalc_test.go`) | `go vet` bersih, `golangci-lint` 0 issues, `go test -race -count=1` untuk `simulate`, `config`, `quality` ok, prettier bersih |
| `make web-e2e` | 8 test Playwright lulus (4 smoke, 4 a11y) |
| `deploy/scripts/smoke.sh` dari volume kosong, termasuk langkah 5b | `SMOKE OK`, 1m43s, dijalankan SESUDAH perubahan Caddyfile terakhir; stack lalu dihapus |
| Percobaan kekacauan (H1, H3, H6, H7) | 960 baris = 960 pesan unik di-ACK, 0 duplikat; broker pulih setelah perbaikan |
| Panduan dev README dijalankan nyata (migrate, seed-dev, `devices.json` lewat awk, `serve`, simulator MQTT 60 dari 60 dan HTTP 15 dari 15 terkonfirmasi, API publik menampilkan level 60,4 cm tidak basi, berhenti bersih) | lulus (pada database sekali pakai di Postgres dev; basis dev milik pemilik tidak disentuh) |

Tidak dijalankan ulang setelah edit dokumen terakhir: `make verify` penuh (hanya berkas `docs/*.md` dan README yang berubah sesudahnya).

Catatan proses: dua kesalahan alat saya sendiri tercatat agar tidak mengotori bukti. Putaran pertama `make verify` pada rantai akhir gagal karena `gosec G304` pada test baru saya (diperbaiki, lalu dijalankan ulang penuh: hijau); putaran pertama panduan dev menyalahgunakan proses `serve` yatim sehingga hasilnya dibuang dan diulang pada proses yang benar.

