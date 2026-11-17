# Protokol telemetri

Sumber kebenaran untuk server (`internal/ingest/protocol`) dan firmware (`firmware/esp32`). Kode dan dokumen ini diuji dengan golden vector yang sama.

## Transport

- MQTT: topik `spillway/v1/{device_key}/telemetry`, QoS 1.
- HTTP: `POST /v1/ingest`, badan JSON yang sama.

## Payload

```json
{"device":"dev-001","sid":"a1b2c3d4","fc":42,"ts":1767225600,"raw_mm":1234,"batt_v":3.71,"sig":"<64 hex>"}
```

| Field | Tipe | Aturan |
|---|---|---|
| `device` | string | `[A-Za-z0-9_-]{1,64}`, sama dengan `device_key` di topik |
| `sid` | string | `[A-Za-z0-9_-]{1,64}`, acak per boot |
| `fc` | uint32 | naik per sesi, 0..4294967295, bilangan bulat tanpa pecahan/eksponen |
| `ts` | int | epoch detik UTC, 0..4102444800 (tahun 2100) |
| `raw_mm` | int | jarak sensor ke permukaan air, 0..100000 (nilai di luar jangkauan kalibrasi tetap diterima dan diberi skor 0) |
| `batt_v` | angka, opsional | 0..20, paling banyak 2 desimal, tanpa eksponen, tanpa tanda; `null` sama dengan tidak ada |
| `sig` | string | tepat 64 karakter hex (huruf besar atau kecil) |

Penolakan ketat: ukuran maks 512 byte, field tak dikenal ditolak, field wajib hilang ditolak, tipe salah ditolak (mis. `"fc":"1"`), data tambahan setelah objek ditolak. Pesan galat tidak pernah memuat isi payload atau signature.

## String kanonik dan signature

```
canonical = device|sid|fc|ts|raw_mm|batt_v
sig       = hex_lowercase( HMAC-SHA256( key, canonical ) )
```

- Encoding UTF-8; bilangan bulat dalam desimal tanpa nol di depan dan tanpa `+`.
- `batt_v` diformat tepat `%.2f` dengan titik desimal (`3.7` menjadi `3.70`, `4` menjadi `4.00`). Jika tidak ada, string kosong (string kanonik berakhir dengan `|`).
- `device` dan `sid` tidak boleh memuat `|` (dijamin oleh pola di atas).
- `key` adalah 32 byte mentah dari secret device. Secret ditampilkan sekali saat registrasi sebagai 64 karakter hex; firmware men-decode hex itu menjadi 32 byte.
- Verifikasi di server memakai perbandingan waktu-konstan. Gagal verifikasi: pesan ditolak, dihitung di metrik, tidak disimpan.

## Golden vector

Secret (hex, 32 byte): `000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f`

Vektor 1 (dengan baterai):

```
canonical: dev-001|a1b2c3d4|42|1767225600|1234|3.71
sig:       cab92fb9c8fc57c187c24b9c38a48251965dff0bc3c8df030332453bb85afd50
payload:   {"device":"dev-001","sid":"a1b2c3d4","fc":42,"ts":1767225600,"raw_mm":1234,"batt_v":3.71,"sig":"cab92fb9c8fc57c187c24b9c38a48251965dff0bc3c8df030332453bb85afd50"}
```

Vektor 2 (tanpa baterai, `batt_v` tidak dikirim):

```
canonical: dev-001|a1b2c3d4|43|1767225660|1230|
sig:       a4e7de1645843cf6d462d828fb170c4fc7d655878a4f8252c154dd690d5b775d
payload:   {"device":"dev-001","sid":"a1b2c3d4","fc":43,"ts":1767225660,"raw_mm":1230,"sig":"a4e7de1645843cf6d462d828fb170c4fc7d655878a4f8252c154dd690d5b775d"}
```

Pemeriksaan independen (tanpa kode proyek):

```sh
printf '%s' 'dev-001|a1b2c3d4|42|1767225600|1234|3.71' \
  | openssl dgst -sha256 -mac HMAC -macopt hexkey:000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f
```

Vektor ini ada di `internal/ingest/protocol/protocol_test.go` (`TestGoldenVectors`) dan wajib dipakai test firmware.

## Idempotensi

Kunci pesan: `(device, sid, fc)`. Tiga lapis, yang terakhir menentukan:

1. Cincin memori per perangkat (128 kunci terakhir, diisi ulang dari DB saat start).
2. Redis: `Seen` sebelum diproses, `Mark` SESUDAH baris di-commit (TTL 24 jam). Menandai sebelum commit (`SET NX` di muka) tidak dipakai: bila proses mati di antara keduanya, kiriman ulang dianggap duplikat dan data hilang (DECISIONS 42). Redis mati atau lambat: pipeline lanjut (fail-open) dengan pemutus sirkuit 5 detik, lapis 3 yang memutuskan.
3. Indeks unik `readings(device_id, ts, sid, fc)` dengan `ON CONFLICT DO NOTHING ... RETURNING`. Hasil akhir yang menentukan apakah sebuah pesan baru atau duplikat.

MQTT: QoS 1, sesi persisten, PUBACK dikirim setelah commit (at-least-once di jaringan, tepat satu baris di penyimpanan). HTTP: jawaban 200 (`stored` atau `duplicate`) hanya setelah commit; 503 dengan `Retry-After` berarti kirim ulang dengan aman. Diuji: `TestIngestMQTTExactResults`, `TestIngestSurvivesRedisOutage`, `TestIngestRestartMidStreamLosesNothing`.

## Aturan waktu (dihitung server, lihat QUALITY.md)

- `waktu_server - ts` > 2 menit: `late`; diterima sampai 24 jam, lebih tua ditolak.
- `ts` lebih dari 10 menit di depan jam server, atau offset besar pada sesi yang jam awalnya tidak tepercaya: `CLOCK_SKEW`.
