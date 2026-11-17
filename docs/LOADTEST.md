# Uji beban

**Status: dijalankan pada 2026-11-17 untuk 1.000 dan 10.000 perangkat, pada satu laptop. Semua angka di bagian "Hasil" adalah pengukuran nyata dari JSON di [loadtest-results/](loadtest-results/); tidak ada yang diekstrapolasi.** Hasil ini BUKAN klaim untuk VPS: ulangi di VPS target dengan perintah di bawah.

## Apa yang diukur

`tools/loadtest` menjalankan N perangkat simulasi lewat MQTT dalam tiga fase: baseline, burst (10x laju baseline selama 60 detik), pemulihan. Per fase dan total, dari pengukuran nyata:

- laju pesan (target dan tersimpan per detik),
- latensi ingest p50/p95/p99: kuantil dari delta bucket histogram `spillway_ingest_e2e_seconds` (terima sampai commit), interpolasi linear seperti `histogram_quantile`,
- kedalaman antrean maksimum (`spillway_ingest_queue_depth`, disampel tiap detik), `backpressure_waits`, galat DB,
- CPU dan memori puncak per layanan (`docker stats`: spillway, postgres, mosquitto, redis),
- pertumbuhan ukuran database,
- kehilangan: pesan unik yang di-ACK broker (laporan simulator) dikurangi baris di tabel `readings` untuk perangkat `load-*`. Harus 0; alat keluar dengan galat bila tidak.

Catatan: "dikonfirmasi" di simulator untuk MQTT berarti PUBACK broker, bukan commit DB. Bukti penyimpanan adalah perbandingan dengan DB. Injeksi galat (duplikat 2 persen, terlambat 1 persen, tidak berurutan 2 persen, bawaan) membuat jumlah baris yang diharapkan = pesan unik, bukan jumlah transmisi.

## Perintah tepat

Prasyarat: Docker, Go (lihat go.mod), `deploy/compose/.env.prod` (di mesin uji: `DOMAIN=localhost`, `PUBLIC_SCHEME=http`, lihat DEPLOY.md bagian smoke test).

```sh
export C="docker compose --env-file deploy/compose/.env.prod -f deploy/compose/docker-compose.prod.yml"
$C up -d --build                                   # tunggu: curl -fsS http://127.0.0.1:8080/readyz
N=1000                                              # lalu 10000 di VPS
( umask 077; $C run --rm -T -e SPILLWAY_ENV=dev spillway seed-load $N /dev/stdout > load-devices.json )
export MQTT_USERNAME=device MQTT_PASSWORD="$(grep ^MQTT_DEVICE_PASSWORD= deploy/compose/.env.prod | cut -d= -f2)"
# 10.000 sensor pada interval nyata 30 detik, burst 10x selama 60 detik (perintah yang dijalankan di bawah):
go run ./tools/loadtest -devices-file load-devices.json -devices $N -mqtt tcp://127.0.0.1:1883 \
  -interval 30s -rate-multiplier 1 -baseline 90s -burst 60s -burst-factor 10 -recovery 60s -out loadtest-result.json
# 1.000 sensor dengan waktu dipadatkan 10x: tambahkan -rate-multiplier 10 -baseline 2m -recovery 1m
```

`load-devices.json` berisi secret perangkat: jangan di-commit (sudah di .gitignore).

`-rate-multiplier 10` memadatkan interval 30 detik menjadi 3 detik (10.000 perangkat = sekitar 3.333 pesan/detik pada baseline, 33.333 pesan/detik pada burst 10x). Itu menuntut mesin pembangkit beban yang kuat: bila laju capaian simulator di laporan JSON jauh di bawah target, yang jenuh adalah pembangkit atau jaringan, bukan Spillway; jalankan `loadtest` dari mesin terpisah (`-mqtt tcp://<vps>:1883` dengan `MQTT_PLAIN_BIND` dibuka sementara, atau 8883) dan turunkan `-rate-multiplier`. Laju nyata 10.000 sensor pada interval 30 detik adalah `-rate-multiplier 1` (333 pesan/detik).

Setelah selesai, salin `loadtest-result.json`, spesifikasi mesin (`lscpu`, `free -h`, versi Docker), dan parameter persis ke bagian "Hasil" di bawah. Reset: `$C down -v`.

## Hasil

Mesin: MacBook (Apple M1, 8 inti, 8 GB RAM), Docker Desktop 29.8.1 dengan VM 8 CPU dan 4 GB, stack `docker-compose.prod.yml` (Postgres/TimescaleDB `timescaledb-ha:pg17` batas 2 GB, Redis, Mosquitto 2, Spillway batas 1 GB), pembangkit beban (`tools/loadtest`) berjalan di host yang sama lewat loopback, sehingga berbagi CPU dengan stack (dan dengan kontainer proyek lain yang menyala). Pada uji 1.000 perangkat seluruh stack hidup (Keycloak, web, Caddy tidak dibebani); pada uji 10.000 perangkat hanya Postgres, Redis, Mosquitto dan Spillway yang dinyalakan. Perangkat berisi 10 per site, satu aturan ambang per site, kalibrasi aktif, kualitas dan aturan dievaluasi per pesan. Injeksi galat: duplikat 2 persen, terlambat 1 persen, tidak berurutan 2 persen. Transport MQTT QoS 1, 16 koneksi pembangkit.

| | 1.000 perangkat, waktu dipadatkan 10x | 10.000 perangkat, interval nyata 30 dtk |
|---|---|---|
| Perintah | `-rate-multiplier 10 -baseline 2m -burst 60s -recovery 1m` | `-rate-multiplier 1 -baseline 90s -burst 60s -recovery 60s` |
| Pesan unik di-ACK broker | 260.000 | 250.000 |
| Baris di DB (org `load`) | 260.000 | 250.000 |
| **Hilang** | **0** | **0** |
| Ditolak (semua fase) | 0 | 0 |
| Galat DB, backpressure | 0, 0 | 0, 0 |
| Waktu pengurasan setelah fase terakhir | sekitar 2 dtk | sekitar 2 dtk |
| Pertumbuhan DB | 22,6 MB ke 91,6 MB (260.000 baris, sekitar 265 byte per baris termasuk indeks dan agregat) | 41,5 MB ke 107,0 MB (250.000 baris) |

Per fase (laju capaian = transmisi per detik di pembangkit, termasuk salinan duplikat; latensi ingest = histogram server terima-sampai-commit):

| Fase | Target pesan/dtk | Capaian pesan/dtk | Latensi ingest p50 / p95 / p99 (ms) | Antrean maks | CPU maks (puncak) Spillway / Postgres | Memori maks Spillway / Postgres |
|---|---|---|---|---|---|---|
| 1.000: baseline | 333 | 349 | 72 / 213 / 420 | 224 | 33% / 22% | 69 / 164 MiB |
| 1.000: burst 10x | 3.333 | 3.411 | 75 / 188 / 374 | 403 | 106% / 58% | 92 / 233 MiB |
| 1.000: pemulihan | 333 | 357 | 92 / 1.365 / 3.666 | 0 | 50% / 38% | 94 / 234 MiB |
| 10.000: baseline | 333 | 507 | 181 / 472 / 505 | 503 | 149% / 150% | 244 / 173 MiB |
| 10.000: burst 10x | 3.333 | 3.563 | 54 / 213 / 372 | 505 | 159% / 71% | 300 / 223 MiB |
| 10.000: pemulihan | 333 | 671 | 238 / 483 / 507 | 507 | 125% / 147% | 304 / 230 MiB |

(CPU 100 persen = satu inti; angka dari `docker stats`, disampel tiap detik.)

### Cara membaca dan batas hasil

- **Tanpa kehilangan:** untuk kedua skala, jumlah pesan unik yang di-ACK broker sama dengan jumlah baris di DB, sudah termasuk 2 persen duplikat (dibuang), 1 persen terlambat (disimpan, ditandai `late`) dan 2 persen tidak berurutan.
- **Pada 10.000 perangkat dengan interval nyata, setiap putaran adalah lonjakan serentak 10.000 pesan** (semua perangkat mengirim pada detik yang sama), lebih keras dari armada nyata yang jamnya tersebar. Karena itu "laju capaian" per fase tampak lebih tinggi dari target (lonjakan diselesaikan dalam beberapa detik, rata-rata dihitung atas waktu fase yang lebih pendek) dan baris yang tersimpan per fase bergeser antar fase (fase baseline 10.000 menyimpan 26.457 dari 30.000, sisanya tercatat di fase berikutnya; hanya total yang bermakna: 250.000 dari 250.000).
- **Resolusi p99:** histogram server berbucket eksponensial (1 ms, 2 ms, ... 512 ms, 1,02 dtk, ...), jadi nilai di sekitar 505 ms pada 10.000 perangkat adalah batas bucket 512 ms, bukan nilai tepat. Kuantil adalah perkiraan interpolasi linear di dalam bucket.
- **Pemulihan 1.000 perangkat p95 1,4 dtk dan p99 3,7 dtk** lebih buruk daripada saat burst, dengan antrean 0 dan tanpa galat. Penyebabnya TIDAK diselidiki (kandidat: pekerjaan latar TimescaleDB seperti refresh continuous aggregate atau kompresi setelah burst, atau rebutan CPU dengan proyek lain di mesin yang sama). Jangan membaca ini sebagai karakteristik tetap.
- **Pembangkit dan stack berbagi satu mesin**, jadi angka CPU/latensi mencampur beban keduanya, dan batas atas sebenarnya Spillway belum ditemukan: tidak ada backpressure, galat, atau antrean menumpuk (maks 507 pesan) sampai 3.563 pesan/detik. Kapasitas maksimum dan perilaku di atas laju itu BELUM diukur.
- Skala 10.000 diuji dengan interval nyata 30 detik dan burst 10x (sekitar 3.300 pesan/detik; ini setara dalam LAJU PESAN dengan 100.000 perangkat pada interval 30 detik, tetapi bukan uji 100.000 perangkat: memori state per perangkat untuk skala itu tidak diukur). Durasi uji pendek (3 sampai 4 menit total): perilaku jangka panjang (kompresi chunk, retensi, pertumbuhan DB berhari-hari) BELUM diukur.
- Kualitas data: setiap pesan dievaluasi kualitas dan aturan ambang; lonjakan simulasi tidak memicu notifikasi karena pengiriman Telegram dimatikan (token kosong) dan level simulasi `steady` tidak melewati ambang.
