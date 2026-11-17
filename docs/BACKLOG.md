# Backlog (ide di luar scope Wajib)

- Jalankan smoke test Playwright di CI (butuh cache browser).
- Row-Level Security Postgres sebagai pertahanan berlapis (`SET LOCAL app.org_id`, role aplikasi bukan owner).
- Job enkripsi ulang secret perangkat setelah rotasi kunci (`Cipher.NeedsRotation` sudah ada).
- Regenerasi secret perangkat (tampil sekali lagi) dari UI admin.
- Worker pengiriman: lewati atau gagalkan notifikasi untuk channel nonaktif (`channels.is_active = false`).
- Uji perilaku `ON CONFLICT` pada chunk terkompresi dan beban `COPY` vs multi-row untuk batch besar (ukur di fase beban).
- Tabel karantina untuk pesan valid yang ditolak karena belum ada kalibrasi (`no_calibration`), agar dapat diproses ulang.
- Shard penulis batch per hash perangkat bila penulis tunggal menjadi bottleneck (ukur di Fase 8).
- Alarm bila `spillway_ingest_queue_depth` tinggi terus-menerus atau Receive Maximum MQTT tercapai.
- Uji bot Telegram dengan token nyata (lihat docs/TELEGRAM.md); belum dijalankan.
- Pengiriman Telegram dengan penandaan `SENT` sebelum kirim ("sending" state) bila jendela duplikat satu-permintaan tidak dapat diterima.
- Pemblokiran `/stop` di grup hanya untuk admin grup (cek `getChatMember`).
- Batas laju global pengirim bersama antar proses (sekarang per proses).
- Hapus site, perangkat, aturan; cabut operator (`DELETE /v1/sites/{id}/operators/{userId}`); daftar operator per site.
- Rate limit terautentikasi per pengguna (selain per IP) dan batas laju untuk endpoint mahal (series).
- Uji alur login browser (code + PKCE) terhadap Keycloak, termasuk refresh token (Fase 7).
- Validasi skema respons terhadap OpenAPI di test kontrak.
- Konfigurasi produksi Keycloak: matikan direct grant, atribut `org` tidak dapat diubah pengguna, HTTPS wajib.
- Fase 7: ganti klien OIDC buatan sendiri dengan `openid-client` v6 setelah dapat diaudit; pindahkan sesi web ke Redis.
- Fase 7: tampilkan severity di GeoJSON publik agar marker peta ikut berwarna menurut alert (sekarang menurut status sensor).
- Fase 7: endpoint tautan langganan Telegram publik per site (kini hanya lewat operator).
- Fase 7: uji aksesibilitas otomatis (axe) di CI; kontras diperiksa manual (komentar di `styles.css`).
- Fase 9: tampilkan di UI admin alert yang masih terbuka padahal sensornya beku (FLATLINE) atau tanpa data valid, agar tidak terbuka tanpa kabar.
- Fase 9: kredensial MQTT per perangkat (Mosquitto dynamic security) menggantikan akun `device` bersama.
- Fase 9: ukur kapasitas dan batas atas di VPS target (di atas 3.563 pesan/detik, durasi berjam-jam, kompresi chunk).
- Fase 9: pindahkan data zona waktu `WIB/WITA/WIT` di editor site ke berkas pesan bila daftar zona bertambah.

- Tampilan: label sumbu grafik menampilkan `200.39999999999998` (format angka) dan ambang baterai `3.299999952316284 V` (float32 dari API); bulatkan di UI. Tren baterai simulasi -144 V/hari adalah artefak simulator.
