// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Package notify delivers alert notifications to Telegram: the message catalog, a minimal
// Bot API client, the long-polling bot and the outbox sender.
package notify

import (
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/sellebeww/spillway/internal/domain"
	"github.com/sellebeww/spillway/internal/quality"
)

// All user-facing text lives here (Indonesian). Telegram messages use parse_mode HTML, so every
// value that comes from data (site and device names) goes through html.EscapeString.
//
//nolint:gosec // user-facing Indonesian text, "token" here is the word for a deep-link token, not a credential
const (
	SafetyLine   = "Alat bantu komunitas, BUKAN sistem peringatan dini resmi. Ikuti arahan BMKG, BNPB, atau BPBD setempat."
	staleLabel   = "DATA TERTUNDA"
	msgHelp      = "Spillway memantau tinggi muka air.\n\nPerintah:\n/start &lt;token&gt; : berlangganan peringatan sebuah titik (token dari tautan undangan)\n/status : level terakhir titik yang Anda ikuti\n/stop : berhenti berlangganan\n\n"
	msgBadToken  = "Token tidak valid, sudah dipakai, atau kedaluwarsa. Minta tautan undangan baru dari pengelola titik."
	msgSubscribe = "Anda berlangganan peringatan <b>%s</b>. Kirim /stop untuk berhenti.\n\n"
	msgStopped   = "Anda tidak lagi menerima peringatan. Kirim /start dengan token baru untuk berlangganan lagi."
	msgNoSub     = "Anda belum berlangganan titik mana pun. Gunakan tautan undangan dari pengelola."
	msgError     = "Terjadi kesalahan sementara. Coba lagi sebentar lagi."
)

// Payload is everything needed to render one alert message; it is stored in the outbox row.
type Payload struct {
	Kind          string   `json:"kind"`
	Severity      int      `json:"severity"`
	Previous      int      `json:"previous"`
	SiteName      string   `json:"site_name"`
	SiteSlug      string   `json:"site_slug"`
	SitePublic    bool     `json:"site_public"`
	TZ            string   `json:"tz"`
	DeviceName    string   `json:"device_name"`
	LevelCm       float64  `json:"level_cm"`
	RateCmPerHour *float64 `json:"rate_cm_per_hour,omitempty"`
	TS            int64    `json:"ts"`
}

func decimal(v float64) string { return strings.Replace(fmt.Sprintf("%.1f", v), ".", ",", 1) }

// LocalTime formats t in the site's time zone, with the zone abbreviation (for example WIB).
func LocalTime(t time.Time, tz string) string {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	return t.In(loc).Format("02-01-2006 15:04 MST")
}

func sev(n int) string { return domain.Severity(n).String() }

// RenderAlert builds the Telegram message of an alert notification (docs/SPEC.md 5.4): severity,
// site, level, rate when known, local time, public page link and the safety statement.
func RenderAlert(p Payload, publicBaseURL string) string {
	var head string
	switch p.Kind {
	case "OPENED":
		head = fmt.Sprintf("PERINGATAN <b>%s</b>", sev(p.Severity))
	case "ESCALATED":
		head = fmt.Sprintf("NAIK ke <b>%s</b> (sebelumnya %s)", sev(p.Severity), sev(p.Previous))
	case "DEESCALATED":
		head = fmt.Sprintf("TURUN ke <b>%s</b> (sebelumnya %s)", sev(p.Severity), sev(p.Previous))
	case "CLOSED":
		head = fmt.Sprintf("Peringatan <b>DITUTUP</b> (sebelumnya %s): level kembali di bawah ambang", sev(p.Previous))
	case "REMINDER":
		head = fmt.Sprintf("PENGINGAT: masih <b>%s</b>", sev(p.Severity))
	default:
		head = "Pembaruan peringatan"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\nTitik: <b>%s</b>\n", head, html.EscapeString(p.SiteName))
	fmt.Fprintf(&b, "Tinggi muka air: %s cm\n", decimal(p.LevelCm))
	if p.RateCmPerHour != nil {
		fmt.Fprintf(&b, "Laju perubahan: %s cm/jam\n", decimal(*p.RateCmPerHour))
	}
	fmt.Fprintf(&b, "Waktu pembacaan: %s\n", LocalTime(time.Unix(p.TS, 0), p.TZ))
	if p.SitePublic && publicBaseURL != "" {
		fmt.Fprintf(&b, "Detail: %s/s/%s\n", strings.TrimRight(publicBaseURL, "/"), html.EscapeString(p.SiteSlug))
	}
	fmt.Fprintf(&b, "\n<i>%s</i>", SafetyLine)
	return b.String()
}

// DelayNote is the prominent first line of an alert that is delivered long after its reading
// (outbox retries while Telegram was unreachable). Empty while the reading is still fresh.
func DelayNote(p Payload, now time.Time) string {
	age := now.Sub(time.Unix(p.TS, 0))
	if age <= quality.StaleAfter(0) { // the 5 minute floor of docs/SPEC.md section 1
		return ""
	}
	return fmt.Sprintf("<b>%s</b>: pesan ini terkirim %d menit setelah pembacaan; kondisi sekarang bisa berbeda.\n", staleLabel, int(age.Minutes()))
}

// StatusLine is one device line of the /status reply. lastSeen is the arrival time of the newest
// reading; stale readings are shown prominently as DATA TERTUNDA with their time.
func StatusLine(device string, level float64, ts time.Time, tz string, stale bool) string {
	if stale {
		return fmt.Sprintf("- %s: <b>%s</b>, terakhir %s cm pada %s", html.EscapeString(device), staleLabel, decimal(level), LocalTime(ts, tz))
	}
	return fmt.Sprintf("- %s: %s cm (%s)", html.EscapeString(device), decimal(level), LocalTime(ts, tz))
}

// NoDataLine is the /status line of a device that never reported.
func NoDataLine(device string) string {
	return fmt.Sprintf("- %s: <b>%s</b>, belum ada data", html.EscapeString(device), staleLabel)
}
