# Progress

## Fase

| Fase | Isi | Status |
|---|---|---|
| 1 | Fondasi repo, infra dev, CI | SELESAI-TERVERIFIKASI (CI GitHub belum dijalankan, lihat catatan) |
| 2 | Logika inti murni (domain, kalibrasi, kualitas, aturan, protokol) | SELESAI-TERVERIFIKASI |
| 3 | Lapisan data: migrasi, Timescale, store, enkripsi, audit, seed | SELESAI-TERVERIFIKASI (dijalankan terhadap TimescaleDB nyata) |
| 4 | Ingest MQTT+HTTP, pipeline, simulator dasar | SELESAI-TERVERIFIKASI (Mosquitto, Redis, TimescaleDB nyata) |
| 5 | Alert end-to-end: sink aturan, outbox, bot dan pengirim Telegram, skenario simulator | SELESAI-TERVERIFIKASI (bot nyata: SELESAI-PERLU-UJI-LINGKUNGAN) |
| 6 | API REST, endpoint publik, SSE, auth JWT/OIDC | SELESAI-TERVERIFIKASI (login browser PKCE: kini diverifikasi di Fase 7) |
| 7 | UI SvelteKit: publik (peta, site), admin, auth OIDC PKCE | SELESAI-TERVERIFIKASI (tangkapan layar: diambil pemilik; tile OSM nyata belum diuji) |
| 8 | Firmware ESP32, simulator lengkap, alat uji beban, deploy produksi, CI | SELESAI-TERVERIFIKASI (smoke compose prod lulus, uji beban 1.000 dan 10.000 perangkat dijalankan, 0 kehilangan); firmware belum diuji di hardware, VPS/TLS/Telegram nyata dan CI GitHub belum diuji |
| 9 | Audit independen, perbaikan, dokumentasi final, tag `v0.1.0-mvp` | SELESAI-TERVERIFIKASI (73 butir audit: awal 55 PASS dan 18 FAIL, akhir 73 PASS dan 0 FAIL, lihat `docs/AUDIT.md`); butir lingkungan nyata tetap SELESAI-PERLU-UJI-LINGKUNGAN (`docs/ENV_TEST.md`) |

## Butir Wajib

Sumber audit tunggal: `docs/AUDIT.md` (Fase 9). Tabel "Pemetaan" per fase di bawah adalah catatan historis; entri "BELUM" di dalamnya sudah dipenuhi fase berikutnya (lihat T-W1 sampai T-57 di AUDIT.md).

| # | Butir | Status | Lokasi |
|---|---|---|---|
| 1 | Registrasi site/device, kalibrasi | SELESAI-TERVERIFIKASI | API (`internal/api/admin.go`, `docs/openapi.yaml`) dan UI admin (`web/src/routes/admin/*`); dijalankan di peramban terhadap Keycloak, API, DB dan simulator nyata (skrip sekali jalan), dan di Playwright dengan API tiruan |
| 2 | Ingest MQTT dan HTTP idempoten | SELESAI-TERVERIFIKASI | `internal/ingest` (`pipeline.go`, `shard.go`, `mqtt.go`, `http.go`, `redis.go`); `integration_test.go` dengan broker, Redis, DB nyata |
| 3 | Skor kualitas data | SELESAI-TERVERIFIKASI | `internal/quality` terhubung di `ingest/shard.go`; flag tersimpan dan terhitung di metrik |
| 4 | Aturan THRESHOLD dan RATE_OF_RISE | SELESAI-TERVERIFIKASI | `internal/rules` + `internal/alerts/sink.go` (rebuild, rekonsiliasi, tick); `alerts_test.go` end-to-end (form aturan di `web/src/routes/admin/rules`, validasi `lib/validation.ts` diuji) |
| 5 | Notifikasi Telegram | SELESAI-PERLU-UJI-LINGKUNGAN | `internal/notify` (bot, pengirim, katalog), `store/alertflow.go`; semua diuji dengan server Telegram tiruan; bot nyata belum diuji (docs/TELEGRAM.md) |
| 6 | Peta, SSE, halaman publik | SELESAI-TERVERIFIKASI | API publik dan SSE (`internal/api/public.go`, `hub.go`); web: `web/src/routes/+page.svelte`, `s/[slug]`; level, grafik, SSE "Langsung" dan label DATA TERTUNDA diamati dengan simulator nyata; marker peta diuji di Playwright (tile tiruan). Tile OSM nyata belum diuji |
| 7 | Auth OIDC | SELESAI-TERVERIFIKASI (API); login browser PKCE SELESAI-TERVERIFIKASI (Keycloak nyata di Chromium: login, cookie httpOnly, token tidak terlihat JS, logout; mock PKCE di Playwright) | `internal/auth/jwt.go`, `internal/api/server.go`; token Keycloak nyata diverifikasi terhadap biner `serve` (JIT, bootstrap admin, operator, 401/403); web: `web/src/lib/server/oidc.ts`, `routes/auth/*` |
| 8 | Simulator dan uji beban | SELESAI-TERVERIFIKASI (simulator); uji beban SELESAI-TERVERIFIKASI pada laptop (1.000 dan 10.000 perangkat, 0 hilang; di VPS belum) | `internal/simulate`, `cmd/simulator` (duplikat, terlambat, tidak berurutan, rate-multiplier, laporan JSON; `simulate_test.go`), `tools/loadtest`, `spillway seed-load`, `docs/LOADTEST.md` (angka nyata, mentah di `docs/loadtest-results/`) |
| 9 | Firmware ESP32 | SELESAI-PERLU-UJI-LINGKUNGAN (compile `pio run` sukses, 9 test host lulus dengan golden vector; belum diuji di hardware) | `firmware/esp32` (README: wiring, kalibrasi ember, batas akurasi) |
| 10 | Deploy Compose + Caddy + CI | SELESAI-PERLU-UJI-LINGKUNGAN (image ter-build, `deploy/scripts/smoke.sh` lulus di Docker lokal dari volume kosong: readyz, ingest simulator lewat Caddy, API dan halaman publik, restart broker dan Postgres, realm Keycloak. Fase 9 menemukan dan memperbaiki: `.env.prod.example` tersembunyi oleh `.gitignore` (job CI smoke pasti gagal), broker crash-loop setelah restart, HSTS tidak ada. CI belum pernah dijalankan di GitHub, VPS, DNS dan TLS publik belum diuji) | `deploy/compose/docker-compose.prod.yml`, `deploy/docker/*`, `web/Dockerfile`, `deploy/caddy`, `deploy/scripts`, `docs/DEPLOY.md`, `.github/workflows/ci.yml` |

## Environment (dicek Fase 1)

macOS arm64, RAM 8 GB. Go 1.27.3 (go.mod meminta 1.27.4, toolchain otomatis), Node 25.2.1, npm 11.6.2. Docker Desktop 29.8.1 dan Compose v5.5.1 TERSEDIA (daemon harus dijalankan dengan `open -a Docker`), jadi test integrasi nyata bisa dijalankan. Dipasang via brew: golangci-lint 2.15.0, goose, libpq (psql di `/opt/homebrew/opt/libpq/bin/psql`, keg-only).

## Pemetaan aturan docs/SPEC.md ke test (Fase 2)

Semua di `internal/...`; "BELUM" berarti bagian itu milik fase persistensi/ingest/notifikasi.

| Aturan | Test |
|---|---|
| 5.1 payload, tanda tangan HMAC waktu-konstan | `protocol`: `TestGoldenVectors`, `TestVerifyFailsWhenAnyFieldChanges`, `TestParseRejectsInvalid`, `TestParseAcceptsBoundaries`, `FuzzParse` |
| 5.1 kunci idempotensi | `protocol`: `TestIdempotencyKey` (Redis dan unique index: BELUM) |
| 5.1 CLOCK_SKEW, LATE | `quality`: `TestClockSkewAndLate` |
| 5.1 data terlambat tidak memicu aturan kecuali dalam 5 menit | `rules`: `TestLateDataPolicy` |
| 5.1 kalibrasi memakai waktu pembacaan | `calibration`: `TestResolve` (kasus "late reading uses old version") |
| 5.1 urutan per perangkat (shard), Redis fail-open | BELUM |
| 5.2 rumus level, versi append-only, valid_from tidak mundur | `calibration`: `TestLevel`, `TestResolve`, `TestResolveSameValidFromPrefersHigherVersion`, `TestValidateAppend` |
| 5.3 OUT_OF_RANGE, LOW_BATTERY, FLATLINE (N=20 vs 19) | `quality`: `TestOutOfRangeBoundaries`, `TestLowBattery`, `TestFlatlineExactlyAtN`, `TestFlatlineResetsOnChange` |
| 5.3 SPIKE (Hampel, lantai MAD, kenaikan nyata, MAD=0) | `quality`: `TestSpikeSingleJump150cm`, `TestSpikeNeedsFiveHistoryValues`, `TestHampelIgnoresRealRapidRise`, `TestHampelMADZeroDoesNotDivideByZero`, `TestOutOfRangeValueDoesNotPolluteHistory` |
| 5.3 skor titik = minimum, rekonstruksi state | `quality`: `TestScoreIsMinimumOfComponents`, `TestRebuildMatchesLive` |
| 5.3 kesehatan, tren baterai, status, data tertunda | `quality`: `TestHealthScore`, `TestBatterySlopePerDay`, `TestStatus`, `TestStaleAfter` |
| 5.3 skor < min_quality tidak dipakai aturan | `rules`: `TestLowQualityReadingsAreIgnored`, `TestRateOfRiseIgnoresLowQuality` |
| 5.4 THRESHOLD konfirmasi 2 dari 3, histeresis, turun 3 pembacaan | `rules`: `TestThresholdSingleSpikeDoesNotOpen`, `TestThresholdOpensThenEscalatesStepwise`, `TestThresholdHysteresisPreventsFlapping`, `TestThresholdDeescalateThenClose`, `TestThresholdReopenAfterClose` |
| 5.4 RATE_OF_RISE OLS 10 menit, >= 4 titik, >= 5 menit, 2 evaluasi, histeresis 20 persen | `rules`: `TestRiseRateOnSyntheticSlopes`, `TestRiseRateMinimumData`, `TestRateOfRiseOpensAndEscalates`, `TestRateOfRiseInsufficientDataNeverOpens`, `TestRateOfRiseHysteresis20Percent`; OLS: `stats`: `TestOLSMatchesExactReference`, `TestOLSNeverNaNOnExtremes` |
| 5.4 cooldown, eskalasi melewati cooldown | `rules`: `TestCooldownSuppressesRemindersButEscalationPasses` |
| 5.4 state dibangun ulang dari riwayat sama dengan live | `rules`: `TestRebuildMatchesLive` (pembacaan dari DB: BELUM) |
| 5.4 isi pesan notifikasi | BELUM |
| Paket inti tanpa DB, jaringan, jam sistem | `domain`: `TestCorePackagesArePure` |

## Pemetaan DoD Fase 3 ke test

Test integrasi di `internal/store` berjalan terhadap `timescale/timescaledb-ha:pg17` (testcontainers).

| Persyaratan | Test |
|---|---|
| Migrasi bersih dari nol, naik-turun-naik, kebijakan terpasang, semua tabel bisnis punya org_id | `TestMigrationsUpAndDown` |
| Retensi dapat dikonfigurasi | `TestRetentionIsConfigurable` |
| Unique index menolak duplikat | `TestUniqueIndexRejectsDuplicates` |
| Batch insert idempoten, jumlah baris benar-benar masuk, batch besar | `TestInsertReadingsIsIdempotent`, `TestReadingsRoundTrip` |
| Continuous aggregate (real-time, termaterialisasi, data terlambat) sama dengan hitung manual | `TestContinuousAggregatesMatchManualCalculation` |
| Pemilihan kalibrasi lewat query sama dengan `calibration.Resolve`, append-only, versi bersamaan | `TestCalibrationSelectionAndAppendOnly` |
| Secret terenkripsi, round-trip, gagal bila kunci salah atau blob dipindah | `TestDeviceSecretIsEncryptedAndBound`, `auth.TestDecryptFailures`, `auth.TestKeyRotation` |
| Audit otomatis, atomik, append-only, tanpa secret | `TestAuditLog` |
| Outbox idempoten, lease, tanpa klaim ganda antar worker; token sekali pakai | `TestOutboxIsIdempotentAndLeased`, `TestConcurrentWorkersNeverShareAClaim`, `TestSubscriptionTokens` |
| Isolasi organisasi untuk setiap repository (baca, tulis, dan tanpa oracle keberadaan) | `TestOrganizationIsolation` |
| Tidak ada query tanpa scope org | `TestIsScoped`, `TestNoUnscopedDatabaseCalls` (AST), guard runtime `ErrUnscoped`; grep manual dicatat di laporan fase |
| Seed dev idempoten | `TestSeedDevIsIdempotent` |

## Pemetaan DoD Fase 4 ke test

| Persyaratan | Test |
|---|---|
| Shard menjaga urutan per perangkat (8 produsen paralel) | `TestPerDeviceOrderIsPreservedUnderConcurrency` |
| Batch writer flush by size, by time, urutan terjaga, tanpa kehilangan | `TestBatcherFlushesBySize`, `TestBatcherFlushesByTime`, `TestBatcherPreservesOrderAndLosesNothing` |
| Backpressure tanpa drop diam-diam; HTTP 503 + Retry-After | `TestBackpressureNeverDropsSilently`, `TestHTTPBusyAnswers503WithRetryAfter` |
| Shutdown menguras tanpa kehilangan; shutdown keras = tepat satu `Failed` per pesan; tanpa deadlock | `TestCloseDrainsEverythingWithoutLoss`, `TestHardShutdownLeavesUnfinishedMessagesFailed`, `TestCloseReleasesBlockedSubmitters` |
| Retry galat DB/lookup tanpa kehilangan; baris beracun terisolasi; duplikat dalam satu batch | `TestTransientDatabaseErrorsAreRetriedWithoutLoss`, `TestTransientLookupErrorsNeverFailAMessage`, `TestPoisonRowDoesNotBlockItsBatch`, `TestSameBatchDuplicateIsStoredAndCountedOnce` |
| Penolakan per alasan, duplikat per lapis, Redis fail-open, flag late/skew, state kualitas dari riwayat | `TestRejectionsAndDuplicates`, `TestRedisLayerAndFailOpen`, `TestLateAndClockSkewFlags`, `TestQualityStateIsRebuiltFromHistory` |
| HTTP: status code, rate limit, tanpa oracle, 2xx = sudah commit | `TestHTTPStatusCodes`, `TestHTTPRateLimitPerDevice`, `TestIngestHTTPEndToEnd`, `TestLimiter*` |
| 10.000 unik + 2.000 duplikat + 500 tidak berurutan + 300 terlambat + 100 sig salah lewat Mosquitto nyata: 10.800 baris tepat, 0 duplikat, 100 ditolak, 300 `late`, 10.800 event pub/sub | `TestIngestMQTTExactResults` |
| Redis mati di tengah aliran: tetap idempoten lewat unique index | `TestIngestSurvivesRedisOutage` |
| Proses mati di tengah aliran: tanpa kehilangan, tanpa duplikat | `TestIngestRestartMidStreamLosesNothing` |
| Tanpa goroutine bocor | `goleak.VerifyNone` pada semua test pipeline unit |
| Simulator: skenario, payload valid, laporan | `internal/simulate` tests; dijalankan nyata terhadap biner `serve` |

Bukti biner nyata: `simulator` via MQTT (akun `device`, ACL aktif) dan HTTP, `/readyz` memuat `mqtt`, 90 baris, 3 perangkat ONLINE, metrik bergerak, SIGTERM menghasilkan "stopped".

## Pemetaan DoD Fase 5 ke test

| Persyaratan | Test |
|---|---|
| Alert dan notifikasi dalam satu transaksi; idempoten; tanpa event bila ditolak | `notify.TestApplyDecisionIsAtomic`, `TestSenderDeliversEachNotificationExactlyOnce` |
| flood-wave: OPENED, ESCALATED x2, REMINDER x2, DEESCALATED x2, CLOSED = 8 notifikasi per channel, sama dengan mesin murni; cooldown | `alerts.TestFloodWaveGivesTheFullAlertLifecycle` |
| Rekonstruksi setelah restart sama dengan live (dua jenis aturan); keputusan hilang sebelum commit dipancarkan setelah restart | `alerts.TestRestartRebuildsTheSameStateAsLive`, `TestDecisionLostBeforeCommitIsReconciledAfterRestart`, `rules.TestAdoptContinuesAnAlertOlderThanTheReplayWindow` |
| spike-noise dan flatline tidak menghasilkan alert palsu (angka, dengan kontrol tanpa gating); stuck-high tanpa spam; dropout; late-burst | `alerts.TestQualityScoringKeepsNoiseFromRaisingAlerts` |
| Pengingat hanya dengan data segar | `rules.TestRemindOnlyWithFreshData` |
| Pesan terkirim sekali meski 4 worker paralel dan keputusan diulang; restart tidak menggandakan; lease worker mati | `notify.TestSenderDeliversEachNotificationExactlyOnce` |
| 429 ditaati (`retry_after`), 403 menonaktifkan channel, batas laju per chat | `notify.TestSenderRetryAfterIsRespected`, `TestSenderDeactivatesBlockedChannelsAndRetriesRateLimits`, `TestSenderPacesMessagesPerChat` |
| Alur subscribe lewat token (sekali pakai), /status dengan DATA TERTUNDA, /stop, offset persisten | `notify.TestBotSubscribeStatusStopFlow`, `TestBotLongPollingPersistsItsOffset`, `TestRevokeSubscriptionTokens` |
| Isi pesan, escape HTML, token tidak bocor | `notify.TestRenderAlertContent`, `TestClientErrorsAndTokenScrubbing`, `config.TestLoadTelegramSettings` |
| Skenario simulator | `simulate.TestNewScenarios` |

Hasil terukur test flood-wave: urutan `[OPENED ESCALATED ESCALATED REMINDER:1 REMINDER:2 DEESCALATED DEESCALATED CLOSED]`, 16 baris outbox untuk 2 channel. Bukti biner nyata: migrasi ke versi 5, `serve` tanpa token mencatat peringatan, `sub-link` mencetak tautan.

## Pemetaan DoD Fase 6 ke test

Semua di `internal/api` kecuali dicatat; test handler memakai JWKS tiruan (`internal/auth/jwttest`) dan DB nyata.

| Persyaratan | Test |
|---|---|
| Tanpa token, kedaluwarsa, issuer atau audience salah, tanda tangan rusak: 401; org tak dikenal: 403 | `TestAuthenticationRejectsBadTokens`; `auth.TestVerifyRejectsEverythingElse` (termasuk alg none dan HS256) |
| JWKS ber-cache, rotasi kunci, IdP mati | `auth.TestJWKSIsCachedAndRefreshedOnRotation` |
| JIT, bootstrap admin sekali, e-mail tak terverifikasi tidak dipercaya | `TestBootstrapAdminOnlyOnce`, `TestUnverifiedEmailCannotImpersonateAnOperatorTarget` |
| Peran salah, operator lintas site | `TestRolesAdminOperatorAndCrossSite` (17 kasus) |
| ADMIN LINTAS ORGANISASI untuk setiap endpoint terautentikasi (baca dan mutasi): 404, data A tidak berubah | `TestCrossOrganizationIsolation` (18 endpoint) |
| Validasi aturan, body, input ketat, paginasi | `TestValidationIsStrict` |
| Secret hanya sekali, tidak ada di GET atau audit | `TestSecretIsShownExactlyOnce` |
| Audit setiap mutasi dengan aktor | `TestEveryMutationIsAudited` |
| Publik tidak membocorkan site non-publik, GeoJSON, label tertunda, tanpa id internal | `TestPublicSitesListsOnlyPublicSitesAndLeaksNothing`, `TestPublicSiteDetailHidesPrivateSites` |
| Resolusi otomatis, batas titik, pembacaan skor rendah tidak tampil | `TestPublicSeriesPicksResolutionAndFiltersLowQuality` |
| Rate limit per IP (XFF tidak dapat dipalsukan), CORS, header keamanan | `TestPublicEndpointsAreRateLimitedPerIP`, `TestCORSAndSecurityHeaders` |
| SSE: hanya event publik tepercaya, heartbeat, Last-Event-ID, batas per IP, bersih saat klien putus dan saat hub berhenti | `hub_test.go` (4 test, `goleak`) |
| Kontrak OpenAPI sinkron dengan router | `TestOpenAPIMatchesTheRouter` |

Bukti nyata (Keycloak di Docker): token `admin.dev` dan `operator.dev` (iss, aud `spillway-api`, org `dev`) diverifikasi oleh biner `serve`: admin bootstrap, operator tanpa peran melihat 0 site dan 403 saat membuat site, setelah ditugaskan melihat 1 site, token dirusak 401, header keamanan ada.

## Pemetaan DoD Fase 7 ke test

| Persyaratan | Bukti |
|---|---|
| Pemilihan resolusi (cermin API), rentang, zona waktu situs, label tertunda, validasi form aturan dan kalibrasi | Vitest: `resolution.spec`, `format.spec`, `stale.spec`, `series.spec`, `validation.spec` (132 test di 10 berkas, semua lulus) |
| OIDC: PKCE (vektor RFC), open redirect, ID token (alg none, HS256, nonce, aud, exp), sesi, CSP, tautan Telegram | `server/oidc.spec`, `session.spec`, `security.spec` |
| SSE tidak bocor: close menghentikan stream dan retry | `live.spec` |
| Peta memuat dengan API tiruan, situs basi menampilkan label (tanpa JavaScript), form kalibrasi menolak input tidak valid dan tidak mengirim apa pun, login PKCE dengan penyedia tiruan yang memeriksa verifier | Playwright `e2e/smoke.e2e.ts` (4 test lulus, Chromium) |
| API tambahan: `last_reading`, ambang/severity/alert publik tanpa id internal | Go `ui_support_test.go` |
| Alur nyata: Keycloak + API + TimescaleDB + simulator `flood-wave` + web (`node build`) | skrip sekali jalan (tidak di repo): level 59,7 cm tampil, "Langsung", grafik, marker, 6 halaman admin 200, audit 18 baris, secret 64 hex tampil sekali lalu hilang setelah muat ulang, logout ke Keycloak lalu /admin kembali ke login; tanpa luapan horizontal di 375 px |

Ukuran bundle klien (gzip, `npm run size`, Vite 8): dasar bersama 53,3 kB; `/` 60,5 kB; `/s/[slug]` 65,5 kB; halaman admin 54,7 sampai 64,3 kB. Lazy: MapLibre 289,1 kB (+ CSS 9,9 kB, hanya peta dan editor site), ECharts modular 173,9 kB (hanya halaman site). Hasil `make verify`: go vet, golangci-lint, `go test -race` (semua paket ok), `npm run check` 0 galat 0 peringatan, lint bersih, 132 test Vitest, build bersih.

Belum dijalankan / batas: tile OSM nyata (hanya tile tiruan), uji aksesibilitas otomatis (kontras dihitung manual, tanpa axe), Playwright belum di CI, tangkapan layar (daftar di `docs/screenshots/README.md`), rate limit API dari server web membutuhkan `TRUST_PROXY_HEADERS=true`.

## Konteks untuk tahap berikutnya
- Fase 1 sampai 9 selesai; tag `v0.1.0-mvp`. Audit: `docs/AUDIT.md` (73 butir, akhir 73 PASS, 0 FAIL). Yang masih butuh pemilik: `docs/ENV_TEST.md` (bot Telegram nyata, ESP32, VPS dan TLS, Keycloak produksi, CI GitHub); alasan di `docs/BLOCKERS.md`.
- Perintah: `make verify` (butuh Docker, 10 sampai 15 menit), `make web-e2e` (8 test Playwright), `make smoke` (stack produksi lewat HTTP; `SPILLWAY_LOCAL_PORT=18080 deploy/scripts/smoke.sh` bila port 8080 terpakai), `make dev-up`, `go run ./cmd/spillway {serve|migrate|seed-dev|sub-link|create-org|seed-load}`, `go run ./cmd/simulator`, `go run ./tools/loadtest`.
- Alur data: ingest -> `alerts.Sink` -> outbox -> Telegram; API membaca lewat `store.ForOrg` dan `PublicSites` (satu-satunya baca publik lintas-org, bertanda `-- unscoped:`). Teks UI hanya di `web/src/lib/messages.ts`; teks pesan Telegram di `internal/notify/messages.go`.
- Keputusan penting Fase 9 (DECISIONS 93 sampai 102): basi menurut umur pengukuran (`quality.IsStaleReading`, `isFreshReading` di web), `notify.DelayNote`, kunci master contoh ditolak di produksi, entrypoint Mosquitto aman restart, HSTS (satu header per host), `.env.prod.example` dilacak, goleak di `alerts` dan `notify`.
- Jebakan: `.env` men-set `MQTT_USERNAME=spillway` (akun baca); simulator perlu `MQTT_USERNAME=device`. Port host 8080/443 mungkin dipakai proyek lain. Keycloak start pertama lambat. `.gitignore` memakai `.env.*` dengan negasi eksplisit untuk contoh. Jangan mematikan PID subshell untuk menghentikan `serve` (proses yatim).
- Batas yang diketahui (README Threat model dan QUALITY.md): akun broker `device` bersama, sesi web di memori, lima pembacaan ekstrem berturut-turut diterima sebagai level baru, sensor macet tinggi dapat membuka satu alert, uji beban hanya di laptop.
- Lingkungan pengembangan: macOS arm64, Docker Desktop (jalankan dengan `open -a Docker`), Go 1.27.4, Node 25. Tidak ada ESP32, token bot, VPS.
