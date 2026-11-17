# Kualitas data

Implementasi: `internal/quality`. Semua fungsi murni; waktu berasal dari pembacaan, bukan jam sistem.

## Skor titik

Setiap pembacaan mendapat daftar flag dan skor titik `0..1`. Skor titik adalah **minimum** skor komponen flag yang aktif.

| Flag | Syarat | Skor |
|---|---|---|
| `OUT_OF_RANGE` | `raw_mm < min_valid_mm` atau `raw_mm > max_valid_mm` (batas inklusif; default 250 dan 4500) | 0,0 |
| `SPIKE` | lihat Hampel di bawah | 0,2 |
| `FLATLINE` | `raw_mm` identik persis pada N = 20 pembacaan berurutan (flag muncul di pembacaan ke-20, tidak di ke-19, dan tetap selama nilai sama) | 0,3 |
| `CLOCK_SKEW` | lihat jam di bawah | 0,5 |
| `LOW_BATTERY` | `batt_v` < ambang perangkat (default 3,3 V; tepat 3,3 tidak ditandai) | 0,9 |
| `LATE` | informasional | 1,0 |
| `OK` | tidak ada flag lain | 1,0 |

Pembacaan dengan skor < `min_quality` (default 0,5) disimpan tetapi tidak dipakai aturan.

## Hampel kausal

Jarak sensor dalam cm (`raw_mm / 10`; bebas dari kalibrasi). Jendela berisi 7 nilai dalam jangkauan terakhir (nilai `OUT_OF_RANGE` tidak masuk jendela). Butuh minimal 5 nilai riwayat.

```
med = median(jendela)
mad = max( MAD(jendela), 0.5 )          # lantai 0,5 cm, mencegah MAD = 0
SPIKE jika |nilai - med| > 3 x 1.4826 x mad
```

Lantai MAD berarti ambang minimum adalah `3 x 1.4826 x 0.5 = 2.2239` cm. Tidak ada pembagian, jadi MAD = 0 aman.

Jendela sengaja ikut memuat nilai yang ditandai spike. Jika tidak, pergeseran muka air yang nyata dan menetap akan ditandai spike selamanya karena jendela tidak pernah beradaptasi. Dengan desain ini, satu lonjakan tetap terdeteksi (median tidak terganggu satu pencilan), dan setelah beberapa pembacaan jendela mengikuti level baru.

Contoh angka:

- Riwayat 250,0 / 250,2 / 249,8 / 250,1 / 249,9 cm: median 250,0, MAD 0,1, dipakai lantai 0,5. Ambang 2,22 cm. Nilai baru 400,0 cm: selisih 150 cm, `SPIKE`, skor 0,2.
- Kenaikan nyata 60 cm/jam dengan sampel 1 menit (1 cm per langkah): jendela 100..106, median 103, MAD 2, ambang 8,9 cm; nilai baru 107 selisih 4 cm, tidak ditandai. Skala ini tidak berubah untuk interval lain (selisih = 4 langkah, ambang = 8,9 langkah). Diuji untuk 60 cm/jam pada interval 1, 5, 10 menit dan 120 cm/jam pada 1 menit.

## Jam: CLOCK_SKEW dan LATE

Misalkan `off = waktu_terima - ts`.

- Pembacaan pertama pada setiap `sid` menentukan apakah jam sesi itu tepercaya: tepercaya jika `|off| <= 10 menit`.
- `CLOCK_SKEW`: `off < -10 menit` (ts di masa depan), atau `off > 10 menit` pada sesi yang tidak tepercaya.
- `LATE`: bukan skew dan `off > 2 menit`. Pada sesi tepercaya, offset besar berarti pesan tertahan (buffer), bukan jam rusak.

Alasan: tanpa pembeda ini, jam perangkat yang belum disinkronkan (umum pada ESP32 tanpa NTP) tidak dapat dibedakan dari pesan terlambat. Batas: jika pembacaan pertama sesi justru pesan tertahan, sesi itu dianggap tidak tepercaya sampai reboot berikutnya.

## Kesehatan dan status perangkat

- Skor kesehatan (jendela 1 jam): `min(1, diterima / diharapkan) x rata-rata(skor titik)`, dengan `diharapkan = jendela / interval_harapan`. Contoh: 45 dari 60 pembacaan diterima, rata-rata skor 0,9: `0,75 x 0,9 = 0,675`.
- Tren baterai: kemiringan OLS `batt_v` terhadap waktu (volt per hari) dari sampel 24 jam terakhir; butuh minimal 2 sampel.
- Status: `ONLINE` jika umur pembacaan terbaru <= 2 x interval; `DELAYED` jika > 2 x interval; `OFFLINE` jika > max(10 x interval, 15 menit). Belum pernah terlihat: `OFFLINE`.
- Data tertunda untuk UI publik dan pesan ("DATA TERTUNDA"): umur > max(2 x interval, 5 menit). Umur dihitung dari yang LEBIH TUA antara `ts` pengukuran dan waktu tiba (`quality.IsStaleReading`). Pembacaan berumur 10 menit yang baru tiba (terlambat atau tertahan di buffer) tetap tampil sebagai DATA TERTUNDA, sedangkan status koneksi (`ONLINE`, `DELAYED`, `OFFLINE`) tetap dihitung dari waktu tiba. Jam perangkat yang jauh tertinggal membuat site tampil tertunda terus (arah yang aman) dan ditandai `CLOCK_SKEW`.
- Pesan Telegram yang baru terkirim lebih dari 5 menit setelah pembacaannya (antrean outbox saat Telegram bermasalah) diawali baris **DATA TERTUNDA** dan selisih menitnya (`notify.DelayNote`).

## Batas yang diketahui (jujur)

- Jendela Hampel ikut memuat nilai yang ditandai spike. Akibatnya empat pembacaan berurutan yang sama-sama ekstrem ditandai `SPIKE`, tetapi yang kelima diterima sebagai tingkat baru (diuji: `TestPersistentLevelShiftIsAcceptedOnceTheWindowAdapts`). Nilai mustahil di luar `[min_valid_mm, max_valid_mm]` tetap `OUT_OF_RANGE` dan tidak pernah diterima.
- `FLATLINE` baru muncul pada pembacaan ke-20. Sensor yang macet di nilai tinggi dapat membuka SATU alert dari 19 pembacaan pertama sebelum terdeteksi; sesudahnya tidak ada pengingat dan alert menunggu data valid (diuji: `TestQualityScoringKeepsNoiseFromRaisingAlerts`, subtes "sensor stuck high").
- Muka air yang benar-benar tenang dapat menghasilkan 20 nilai `raw_mm` identik pada resolusi 1 mm. Pembacaan itu ditandai `FLATLINE` (skor 0,3) dan tidak dipakai aturan sampai nilai berubah. Ini keputusan terkunci (N = 20); pantau di UI kesehatan perangkat bila sensor berada di air sangat tenang.

## Pembangunan ulang state

`quality.Rebuild(cfg, riwayat)` memutar ulang pembacaan secara berurutan dan menghasilkan state identik dengan evaluator langsung (diuji pada beberapa titik potong). Riwayat harus memuat minimal max(N flatline, jendela Hampel) pembacaan terakhir ditambah pembacaan pertama sesi berjalan.
