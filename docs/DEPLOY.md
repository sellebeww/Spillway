# Deploy

Satu server, Docker Compose, Caddy (TLS otomatis). Tidak ada deploy otomatis dari CI: semua langkah di bawah dijalankan pemilik.

Status jujur: stack ini diuji naik dan lulus `deploy/scripts/smoke.sh` di mesin pengembang (HTTP, `DOMAIN=localhost`), termasuk restart broker dan Postgres (langkah 5b). TLS publik, DNS, dan bot Telegram nyata BELUM diuji (butuh VPS dan domain): SELESAI-PERLU-UJI-LINGKUNGAN. Langkah uji untuk pemilik ada di [ENV_TEST.md](ENV_TEST.md).

Pengaman bawaan yang perlu diketahui: `spillway` menolak start di produksi bila `SPILLWAY_SECRET_KEY` sama dengan kunci contoh di `.env.example` (kunci itu publik); Caddy mengirim HSTS (`max-age=31536000`) untuk domain utama dan `auth.`; semua layanan `restart: unless-stopped` dan broker pulih sendiri setelah restart.

## 1. Server dari nol

- Ukuran server: BELUM diukur di VPS. Satu-satunya pengukuran adalah di laptop (8 inti, VM Docker 4 GB, lihat [LOADTEST.md](LOADTEST.md)): 10.000 perangkat tanpa kehilangan, tanpa Keycloak dan web aktif. Sebagai titik awal pakai 2 vCPU, 4 GB RAM, 40 GB disk (batas memori di `docker-compose.prod.yml` berjumlah sekitar 5,7 GB bila semuanya penuh), lalu ukur sendiri dengan perintah di LOADTEST.md dan catat hasilnya. Ubuntu 24.04 LTS.
- Firewall: buka 22, 80, 443, dan 8883 (MQTT TLS). JANGAN buka 1883 ke internet kecuali Anda paham kredensial lewat jaringan tanpa enkripsi.
- Pasang Docker Engine dan plugin Compose dari dokumentasi resmi Docker. Buat pengguna non-root yang masuk grup `docker`, matikan login SSH dengan sandi.
- `git clone` repo ini ke server.

## 2. DNS

Dua rekaman A (dan AAAA bila ada IPv6) ke IP server:

```
spillway.example.org       A  <ip>
auth.spillway.example.org  A  <ip>
```

Caddy mengurus sertifikat Let's Encrypt otomatis begitu DNS menunjuk ke server dan port 80/443 terbuka.

## 3. Konfigurasi

```sh
cp deploy/compose/.env.prod.example deploy/compose/.env.prod
chmod 600 deploy/compose/.env.prod
```

Ganti semua `CHANGE_ME` (`openssl rand -hex 24`; `SPILLWAY_SECRET_KEY` dengan `openssl rand -base64 32`), isi `DOMAIN`, `ACME_EMAIL`, `BOOTSTRAP_ADMIN_EMAIL`. **Cadangkan `SPILLWAY_SECRET_KEY` terpisah dari dump database**: tanpanya secret perangkat tidak bisa didekripsi dan semua sensor harus didaftar ulang.

```sh
export C="docker compose --env-file deploy/compose/.env.prod -f deploy/compose/docker-compose.prod.yml"
$C up -d --build --wait
curl -fsS http://127.0.0.1:8080/readyz     # dari server: "ready"
```

Migrasi database berjalan otomatis (layanan `migrate`). Pertama kali, pembangunan image memakan beberapa menit.

## 4. Organisasi dan admin pertama

1. Buat organisasi (sekali): `$C run --rm spillway create-org main "Nama Organisasi"` (slug sama dengan `DEFAULT_ORG_SLUG`).
2. Buat pengguna di Keycloak. Konsol admin sengaja tidak lewat Caddy: dari laptop `ssh -L 8081:127.0.0.1:8081 user@server`, buka `http://localhost:8081`, masuk dengan `KEYCLOAK_ADMIN`, pilih realm `spillway` (diimpor otomatis dari `deploy/keycloak/realm-prod.json`), Users > Add user, isi e-mail yang sama dengan `BOOTSTRAP_ADMIN_EMAIL`, centang "Email verified", lalu tab Credentials > Set password.
3. Buka `https://spillway.example.org/admin`, masuk. E-mail terverifikasi itu otomatis menjadi admin organisasi (hanya bila organisasi belum punya admin). Pengguna lain: buat di Keycloak, lalu tugaskan lewat UI admin.
4. Pengaturan klaim `org`: realm produksi memberi semua pengguna klaim `org=DEFAULT_ORG_SLUG` (satu organisasi). Multi-organisasi butuh atribut pengguna yang tidak dapat diubah pengguna sendiri; di luar scope saat ini.

Ganti sandi admin Keycloak bawaan (`KEYCLOAK_ADMIN_PASSWORD`) hanya lewat konsol; mengubah `.env.prod` setelah bootstrap tidak mengubahnya.

## 5. Sensor

Daftarkan site dan perangkat di UI admin (secret tampil sekali), isi `firmware/esp32/include/secrets.h` ([README firmware](../firmware/esp32/README.md)). Broker:

- Plain MQTT hanya di loopback server (`MQTT_PLAIN_BIND=127.0.0.1`). Sensor di internet harus memakai TLS di 8883.
- TLS Mosquitto: taruh `fullchain.pem` dan `privkey.pem` untuk domain broker (mis. `mqtt.spillway.example.org`, rekaman A ke server; terbitkan dengan certbot `--standalone` saat port 80 bebas, atau salin dari penyimpanan Caddy) di `deploy/mosquitto/certs/` (atau `MQTT_CERT_DIR`), lalu `$C restart mosquitto`. Log memuat "TLS listener enabled on 8883". Perpanjangan sertifikat butuh `restart mosquitto` (cron bulanan). Firmware: `MQTT_URI "mqtts://mqtt.spillway.example.org:8883"` dan `MQTT_CA_PEM` (CA Let's Encrypt).
- Akun `device` bersama: siapa pun yang memegang kredensialnya dapat mengirim ke topik perangkat mana pun, tetapi pesan tetap butuh HMAC per perangkat yang valid (README, Threat model).

## 6. Telegram

Buat bot lewat @BotFather (`/newbot`), isi `TELEGRAM_BOT_TOKEN` dan `TELEGRAM_BOT_USERNAME` di `.env.prod`, `$C up -d spillway`. Langganan lewat tautan di UI admin; detail di [TELEGRAM.md](TELEGRAM.md).

## 7. Backup dan pemulihan

```sh
deploy/scripts/backup.sh                 # ./backups/{spillway,keycloak}-<waktu>.dump, simpan 14 hari
```

Cron harian (sesuaikan path): `15 2 * * * cd /home/user/spillway && deploy/scripts/backup.sh >> backups/backup.log 2>&1`. Salin folder `backups/` ke luar server (rsync, rclone ke penyimpanan objek) dan simpan `SPILLWAY_SECRET_KEY` terpisah.

Uji pemulihan secara berkala ke database cadangan:

```sh
deploy/scripts/restore.sh backups/spillway-<waktu>.dump spillway_restore
```

Skrip memanggil `timescaledb_pre_restore()` dan `timescaledb_post_restore()` (wajib untuk TimescaleDB). Memulihkan ke produksi: `$C stop spillway web`, hapus dan buat ulang database `spillway`, jalankan skrip dengan target `spillway`, `$C up -d`. Server baru: ulangi bagian 1 sampai 3 dengan `.env.prod` dan `SPILLWAY_SECRET_KEY` yang sama, jalankan `$C up -d postgres`, pulihkan, lalu `$C up -d`.

## 8. Rotasi secret

- `SPILLWAY_SECRET_KEY`: naikkan `SPILLWAY_SECRET_KEY_VERSION`, isi kunci baru, pindahkan kunci lama ke `SPILLWAY_SECRET_KEY_OLD=<versi>:<base64>`, `$C up -d spillway`. Kunci lama tetap dapat mendekripsi.
- Secret perangkat: rotasi lewat UI admin (secret baru tampil sekali), flash ulang `secrets.h`.
- Kata sandi Postgres/MQTT/Redis: ubah di `.env.prod` lalu `$C up -d`; untuk Postgres ubah juga di database (`ALTER ROLE`) karena `.env.prod` hanya dibaca saat volume kosong. Kredensial MQTT perangkat berubah berarti semua sensor di-flash ulang.
- Token bot Telegram: revoke di @BotFather, ganti di `.env.prod`, `$C up -d spillway`.

## 9. Operasi

- Log: `$C logs -f spillway` (JSON, tanpa secret). Metrik: `curl http://127.0.0.1:8080/metrics` dari server (tidak publik).
- Pembaruan: `git pull && $C up -d --build`. Spillway berhenti dengan pengurasan (hingga 40 detik); pesan MQTT yang belum di-ACK tetap antre di broker.
- Reset total (HAPUS DATA): `$C down -v`.
- Smoke test lokal tanpa domain: salin `.env.prod.example` ke `.env.prod` dengan `DOMAIN=localhost`, `PUBLIC_SCHEME=http`, lalu `deploy/scripts/smoke.sh`. Bila port 8080 di mesin itu terpakai, jalankan `SPILLWAY_LOCAL_PORT=18080 deploy/scripts/smoke.sh` (port loopback API; nilai yang sama berlaku untuk `curl .../readyz` di panduan ini).
- Pemulihan: broker, Postgres dan Redis boleh restart kapan saja. Pesan MQTT yang belum di-ACK tetap antre di broker (sesi persisten) dan Spillway tersambung ulang sendiri; `/readyz` kembali `ready` setelah semua dependensi pulih. Diukur pada percobaan kekacauan (Postgres mati 20 dtk, broker mati 8 dtk, `kill -9` proses): 960 pesan unik dikonfirmasi broker, 960 baris di DB, 0 duplikat (docs/AUDIT.md).

## Batas yang diketahui

Sesi web di memori (restart web mengeluarkan semua pengguna, satu instance saja); satu server tanpa HA; tile OSM bawaan hanya untuk demo kecil (kebijakan penggunaan OSM); Keycloak memakai Postgres yang sama dengan data aplikasi (pisah database, bukan pisah server).
