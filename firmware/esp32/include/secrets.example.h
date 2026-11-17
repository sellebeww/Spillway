// Copy to secrets.h (git-ignored) and fill in. Never commit secrets.h.
#pragma once

#define WIFI_SSID "your-wifi"
#define WIFI_PASSWORD "your-wifi-password"

// MQTT broker. Use mqtts://host:8883 with MQTT_CA_PEM when the broker has TLS (see docs/DEPLOY.md).
#define MQTT_URI "mqtt://192.168.1.10:1883"
#define MQTT_USERNAME "device"
#define MQTT_PASSWORD "device-dev-password"
// Leave undefined for plain MQTT. For TLS paste the CA certificate that signed the broker.
// #define MQTT_CA_PEM "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----\n"

// Device identity from the admin UI (the secret is shown once, 64 hex characters).
#define DEVICE_KEY "dev-001"
#define DEVICE_SECRET_HEX "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
