# Uji lingkungan nyata (untuk pemilik)

Butir di bawah tidak dapat dibuktikan oleh agen: butuh bot Telegram nyata, ESP32 nyata, VPS dengan domain, atau Keycloak produksi. Setiap uji berisi perintah, hasil yang diharapkan, dan tempat mencatat. Salin tabel di akhir dokumen, isi LULUS atau GAGAL beserta tanggal, lalu perbarui `docs/PROGRESS.md`. Bila ada yang GAGAL, tulis gejala dan keluaran log di `docs/BLOCKERS.md`; jangan menandai butir terkait `SELESAI-TERVERIFIKASI`.

Prasyarat umum: repo ter-clone, Docker, Go (versi di `go.mod`), Node 24+. Jangan tempel token, secret perangkat, atau sandi ke chat, issue, atau log.

## A. Bot Telegram nyata (butir Wajib 5)

Siapkan bot lewat langkah 1 di [TELEGRAM.md](TELEGRAM.md). Lalu:

```sh
make dev-up && make migrate
set -a; . ./.env; set +a
go run ./cmd/spillway seed-dev | tee /tmp/seed.txt           # dicetak sekali: simpan secret perangkat
export TELEGRAM_BOT_TOKEN=... TELEGRAM_BOT_USERNAME=nama_bot_anda PUBLIC_BASE_URL=http://localhost:3000
go run ./cmd/spillway sub-link dev kali-contoh               # cetak https://t.me/<bot>?start=<token>
go run ./cmd/spillway serve                                  # terminal 1
```

| # | Langkah | Hasil yang diharapkan |
|---|---|---|
| A1 | Buka tautan `sub-link` di Telegram, tekan Start | Bot membalas konfirmasi berlangganan "Kali Contoh" dan pernyataan keselamatan |
| A2 | Buka tautan yang sama sekali lagi | "Token tidak valid, sudah dipakai, atau kedaluwarsa" |
| A3 | Kirim `/status` sebelum ada data | Baris perangkat bertanda **DATA TERTUNDA**, "belum ada data" |
| A4 | Buat `devices.json` dari `/tmp/seed.txt` lalu jalankan simulator (di bawah), kirim `/status` | Level terakhir tanpa label tertunda |
| A5 | Simulator `flood-wave` selama 5 menit | Pesan berurutan: PERINGATAN WASPADA, NAIK ke SIAGA, NAIK ke AWAS (waktu lokal WIB, level dengan koma desimal, tautan halaman publik, kalimat "BUKAN sistem peringatan dini resmi"). Pengingat tidak muncul (cooldown 30 menit) |
| A6 | Hentikan simulator, tunggu lebih dari 5 menit, kirim `/status` | **DATA TERTUNDA** dengan waktu pembacaan terakhir |
| A7 | Kirim `/stop`, jalankan simulator lagi | Tidak ada pesan baru |
| A8 | Blokir bot di Telegram, lalu picu satu alert baru | Log `spillway` mencatat channel dinonaktifkan (403); metrik `spillway_notify_channels_deactivated_total` naik 1 |
| A9 | Putus jaringan server 10 menit saat alert terbuka, sambungkan lagi | Pesan yang tertahan terkirim dengan baris awal **DATA TERTUNDA** dan "terkirim N menit setelah pembacaan" |

```sh
printf '[' > devices.json; awk -F'[ =]' '/^device=/{printf "%s{\"device\":\"%s\",\"secret\":\"%s\"}", (n++?",":""), $2, $6}' /tmp/seed.txt >> devices.json; printf ']' >> devices.json
go run ./cmd/simulator -devices-file devices.json -http http://127.0.0.1:8080/v1/ingest -scenario flood-wave -interval 1s -duration 5m
```

`devices.json` berisi secret: jangan di-commit (sudah di `.gitignore`).

## B. ESP32 nyata di ember (butir Wajib 9)

Baca [firmware/esp32/README.md](../firmware/esp32/README.md) (kabel, kalibrasi, batas akurasi). Perangkat: ESP32 DevKit, JSN-SR04T (atau HC-SR04 dengan pembagi tegangan di ECHO), ember tinggi minimal 40 cm, penggaris.

| # | Langkah | Hasil yang diharapkan |
|---|---|---|
| B1 | Daftarkan perangkat di UI admin, salin secret (tampil sekali) ke `firmware/esp32/include/secrets.h` | Secret 64 karakter hex |
| B2 | `cd firmware/esp32 && pio test -e native && pio run -t upload && pio device monitor` | 9 test host lulus; di serial: self-test HMAC lulus, Wi-Fi tersambung, NTP sinkron, baris tiap 30 detik berisi `raw_mm` |
| B3 | Lihat UI admin, halaman perangkat | Pembacaan pertama tiba dalam 1 menit, status ONLINE, skor kesehatan mendekati 1 |
| B4 | Isi ember sampai 10, 20, 30 cm (ukur penggaris), catat `raw_mm` tiap kedalaman. `offset_cm = h + raw_mm / 10` | Tiga nilai `offset_cm` saling berbeda paling banyak sekitar 1 cm. Bila ada tren naik atau turun, sensor tidak lurus ke bawah |
| B5 | Tambah versi kalibrasi dengan rata-rata `offset_cm` | Pratinjau level di admin sama dengan kedalaman penggaris dalam kira-kira 1 cm |
| B6 | Tambah aturan THRESHOLD site, mis. 15 cm WASPADA dan 25 cm SIAGA; isi ember melewati 15 cm lalu 25 cm | Alert terbuka lalu naik; level di halaman publik mengikuti; bila bot dikonfigurasi, pesan Telegram tiba |
| B7 | Kosongkan ember perlahan | DEESCALATED lalu CLOSED setelah 3 pembacaan di bawah ambang dikurangi 5 cm |
| B8 | Cabut sensor atau Wi-Fi 6 menit | Halaman publik menampilkan banner **DATA TERTUNDA** dengan waktu pembaruan terakhir; pulih sendiri saat tersambung |
| B9 | Cabut Wi-Fi 3 menit lalu pasang kembali | Pembacaan tertahan tiba bertanda `LATE`, tidak menggandakan baris (periksa grafik) |

Catat juga suhu udara saat uji: firmware mengasumsikan 28 C (`AMBIENT_TEMP_C`); selisih 10 C kira-kira 1,7 persen jarak.

## C. VPS, domain, TLS (butir Wajib 10)

Ikuti [DEPLOY.md](DEPLOY.md) bagian 1 sampai 5.

| # | Langkah | Hasil yang diharapkan |
|---|---|---|
| C1 | DNS A untuk `DOMAIN` dan `auth.DOMAIN`, `docker compose ... up -d --build --wait` | Semua layanan sehat; sertifikat Let's Encrypt terbit (`docker compose logs caddy`) |
| C2 | `curl -sI https://DOMAIN/` | HTTP/2 200, header `strict-transport-security: max-age=31536000`, `content-security-policy`, `x-content-type-options: nosniff`, `x-frame-options: DENY` |
| C3 | `curl -s -o /dev/null -w '%{http_code}' https://DOMAIN/metrics` dan `/readyz` | 404 keduanya |
| C4 | `curl -sI https://auth.DOMAIN/admin/` | 404 (konsol admin Keycloak tidak publik) |
| C5 | Isi di `.env.prod` kunci contoh dari `.env.example` lalu `up -d spillway` | `spillway` menolak start dengan pesan "published development key" (lalu kembalikan kunci acak Anda) |
| C6 | Dari luar server: `nc -zv IP 1883` | Ditolak atau timeout (MQTT polos hanya loopback). `nc -zv IP 8883` terbuka bila sertifikat broker dipasang |
| C7 | Firmware dengan `mqtts://...:8883` dan `MQTT_CA_PEM` | Pembacaan tiba; dengan sertifikat salah, sambungan ditolak |
| C8 | `deploy/scripts/backup.sh`, lalu `restore.sh` ke database `spillway_restore` | Jumlah baris `readings` sama; secret perangkat tetap terbaca (kunci master sama) |
| C9 | `sudo reboot`, tunggu | Semua kontainer kembali sehat tanpa campur tangan, termasuk `mosquitto`; `/readyz` `ready` |
| C10 | Dari mesin lain: ulang uji beban ([LOADTEST.md](LOADTEST.md)) dengan N dan parameter Anda | `lost = 0`; salin spesifikasi VPS dan JSON hasil ke LOADTEST.md |
| C11 | Buka peta publik dengan tile OSM bawaan | Tile tampil dengan atribusi; untuk lalu lintas nyata ganti `TILE_URL` ke penyedia sendiri (kebijakan OSM) |

## D. Keycloak produksi (butir Wajib 7)

| # | Langkah | Hasil yang diharapkan |
|---|---|---|
| D1 | SSH tunnel ke konsol (DEPLOY.md bagian 4), buat pengguna dengan "Email verified", sandi, e-mail sama dengan `BOOTSTRAP_ADMIN_EMAIL` | Pengguna terbuat di realm `spillway` |
| D2 | Buka `https://DOMAIN/admin`, masuk | Kembali ke `/admin/sites`; di DevTools cookie sesi `httpOnly` dan `Secure`, tidak ada token di Local/Session Storage maupun di JavaScript (`document.cookie` tidak memuatnya) |
| D3 | Pengguna kedua tanpa penugasan site | Melihat 0 site dan ditolak (403) saat membuat site; setelah ditugaskan sebagai operator melihat site itu saja |
| D4 | Ubah atribut `org` pengguna di konsol lalu masuk ulang | Klaim `org` tetap `DEFAULT_ORG_SLUG` (mapper hardcoded) |
| D5 | Keluar | Diarahkan ke Keycloak lalu kembali; `/admin` meminta masuk lagi |
| D6 | Coba `grant_type=password` ke endpoint token klien `spillway-web` | Ditolak (direct access grant mati) |
| D7 | Salah sandi berkali-kali | Akun terkunci sementara (perlindungan brute force) |

## E. Pencatatan hasil

| Uji | Hasil | Tanggal | Catatan |
|---|---|---|---|
| A (bot nyata) | | | |
| B (ESP32 di ember) | | | |
| C (VPS, TLS, backup, reboot) | | | |
| D (Keycloak produksi) | | | |
| CI GitHub (push ke `main`, semua job hijau) | | | |
