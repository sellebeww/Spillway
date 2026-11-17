# Telegram: bot, langganan, dan pengujian

Spillway mengirim peringatan lewat bot Telegram. Bot memakai long polling `getUpdates` (tanpa webhook, tanpa alamat publik).

## 1. Membuat bot (BotFather)

1. Buka Telegram, cari `@BotFather`, kirim `/newbot`.
2. Beri nama tampilan (mis. "Spillway Kali Contoh") dan username unik berakhiran `bot` (mis. `spillway_kali_bot`).
3. BotFather membalas dengan token berbentuk `123456789:AA...`. Perlakukan token seperti kata sandi: jangan commit, jangan tempel di chat atau log. Jika bocor, kirim `/revoke` ke BotFather.
4. Opsional: `/setdescription` dan `/setabouttext` untuk memuat pernyataan keselamatan; `/setcommands` dengan isi:

```
start - berlangganan dengan token undangan
status - level terakhir titik yang diikuti
stop - berhenti berlangganan
```

## 2. Variabel lingkungan

| Variabel | Arti |
|---|---|
| `TELEGRAM_BOT_TOKEN` | token dari BotFather. Kosong berarti bot dan pengirim tidak berjalan: alert tetap tersimpan dan antrean outbox terisi, tidak ada yang terkirim (peringatan dicatat saat `serve` start). |
| `TELEGRAM_BOT_USERNAME` | username bot tanpa `@`; wajib bila token diisi (dipakai untuk tautan undangan). |
| `PUBLIC_BASE_URL` | basis URL halaman publik (tanpa garis miring akhir). Tautan `{PUBLIC_BASE_URL}/s/{slug}` ditambahkan ke pesan hanya untuk site yang `is_public`. |
| `TELEGRAM_API_URL` | default `https://api.telegram.org`; ubah hanya untuk server tiruan saat uji. |

Token tidak pernah masuk log: galat jaringan dibersihkan dari token dan `Config` menyembunyikannya (ada test untuk keduanya).

## 3. Mencoba dengan bot nyata

Daftar uji lengkap dengan hasil yang diharapkan: [ENV_TEST.md](ENV_TEST.md) bagian A. Status: kode dan test dengan server Telegram tiruan sudah lulus; uji dengan bot nyata belum dijalankan oleh agen (tidak punya token), jadi butir ini `SELESAI-PERLU-UJI-LINGKUNGAN`. Langkah:

```sh
make dev-up && make migrate
SPILLWAY_ENV=dev go run ./cmd/spillway seed-dev          # catat secret perangkat (tampil sekali)
export TELEGRAM_BOT_TOKEN=... TELEGRAM_BOT_USERNAME=spillway_kali_bot PUBLIC_BASE_URL=http://localhost:3000
set -a; . ./.env; set +a
go run ./cmd/spillway sub-link dev kali-contoh            # cetak https://t.me/<bot>?start=<token>
go run ./cmd/spillway serve                               # terminal 1
```

1. Buka tautan dari `sub-link` di Telegram dan tekan Start: bot membalas konfirmasi berlangganan. Token sekali pakai dan kedaluwarsa 24 jam; memakainya lagi dijawab "token tidak valid".
2. Kirim `/status`: daftar perangkat site dan level terakhir; data lebih tua dari `max(2 x interval, 5 menit)` ditandai **DATA TERTUNDA** beserta waktunya.
3. Jalankan simulator dengan perangkat dari `seed-dev` (buat `devices.json` berisi `[{"device":"dev-001","secret":"<hex>"}]`):

```sh
go run ./cmd/simulator -devices devices.json -http http://127.0.0.1:8080/v1/ingest \
  -scenario flood-wave -interval 1s -duration 5m
```

   Satu gelombang selesai dalam satu run, sehingga OPENED, ESCALATED, DEESCALATED, dan CLOSED muncul berurutan. Cooldown pengingat (30 menit) dihitung dari waktu pembacaan yang diterima server, jadi dalam run singkat pengingat tidak akan muncul; uji otomatis memakai garis waktu virtual 5 jam untuk memeriksanya.
4. `/stop` menghentikan semua langganan chat itu. Memblokir bot membuat Telegram menjawab 403 pada pengiriman berikutnya; Spillway menonaktifkan channel itu dan berhenti mengirim.

## 4. Perilaku pengiriman

- Satu baris outbox per (alert_event, kind, channel, seq); `seq` bertambah untuk setiap keputusan sebuah event, jadi ESCALATED kedua adalah notifikasi tersendiri, sedangkan pengulangan keputusan yang sama tidak menggandakan.
- Worker mengambil baris dengan `FOR UPDATE SKIP LOCKED` dan lease 2 menit: beberapa worker atau proses tidak mengirim baris yang sama. Worker yang mati sebelum mengirim melepas barisnya sendiri setelah lease.
- 429: menunggu `retry_after` lalu mencoba lagi, dan seluruh pengirim ikut melambat. 403: channel dinonaktifkan, baris `FAILED`. 4xx lain: `FAILED`. 5xx atau jaringan: backoff 5 detik dan seterusnya sampai 15 menit, menyerah setelah 8 percobaan.
- Batas laju: sekitar 1 pesan per detik per chat dan 25 per detik global (per proses).
- Fail-visible: pesan alert yang baru terkirim lebih dari 5 menit setelah waktu pembacaannya (Telegram tidak terjangkau, 429, antrean) diawali baris tebal **DATA TERTUNDA** dengan selisih menitnya (`notify.DelayNote`, diuji `TestDelayNoteMarksLateDeliveries`). `/status` menandai DATA TERTUNDA berdasarkan umur pengukuran, bukan hanya waktu tiba.
- Batas yang jujur: jika pesan sudah diterima Telegram tetapi proses mati sebelum baris ditandai `SENT` (atau penandaan gagal), pesan dikirim ulang setelah lease. Telegram tidak punya kunci idempotensi, jadi jendela ini hanya selebar satu permintaan dan tidak dapat dihilangkan sepenuhnya.
- Metrik: `spillway_notify_sent_total`, `spillway_notify_retries_total{reason}`, `spillway_notify_failed_total{reason}`, `spillway_notify_channels_deactivated_total`, `spillway_notify_outbox_pending`, `spillway_notify_outbox_lag_seconds`.

## 5. Keamanan dan privasi

- Semua teks dari data (nama site, perangkat) di-escape HTML; pesan memakai `parse_mode=HTML`.
- Di grup, setiap anggota dapat mengirim `/stop` yang menghentikan langganan grup itu. Batasi pembuatan tautan undangan untuk grup dan jangan bagikan tautan di tempat umum.
- Tautan undangan dapat dicabut (`RevokeSubscriptionTokens`) dan hanya hash token yang disimpan.
- Setiap pesan memuat pernyataan keselamatan: alat bantu komunitas, bukan sistem peringatan dini resmi; nilai ambang ditetapkan pihak berwenang setempat.
