# Screenshots

Real captures from the development stack: API, TimescaleDB, Mosquitto, Keycloak (development realm), the web app, and the simulator `flood-wave` scenario. All data is simulated. Desktop width 1280 px unless noted; admin pages after a real OIDC login.

| File | Screen | Shows |
|---|---|---|
| `01-peta.png` | `/` | public map with the site marker and live list |
| `02-site-normal.png` | `/s/kali-contoh` | normal state: level gauge, chart with threshold lines |
| `03-site-siaga.png` | `/s/kali-contoh` | flood peak: AWAS badge, active alerts, rate of rise |
| `04-site-tertunda.png` | `/s/kali-contoh` | no data for 5 minutes: DATA TERTUNDA banner |
| `05-site-ponsel.png` | `/s/kali-contoh` | same page at 375 px |
| `06-admin-sites.png` | `/admin/sites` | site list |
| `07-admin-perangkat.png` | `/admin/devices` | device health score, battery trend, last reading |
| `08-admin-kalibrasi.png` | `/admin/calibration` | calibration versions and preview |
| `09-admin-aturan.png` | `/admin/rules` | tiered threshold and rate-of-rise rules |
| `10-admin-alert.png` | `/admin/alerts` | alert events |
| `11-admin-audit.png` | `/admin/audit` | audit log with before and after |
| `12-login-keycloak.png` | Keycloak | code flow with PKCE |

To retake them: run the stack as in the main README (`make dev-up`, `serve`, `make web-dev`, simulator `flood-wave` over 4 minutes), then capture with Playwright at the times above. Not captured: the one-time device secret and Telegram invite pages, on purpose (they show credentials).
