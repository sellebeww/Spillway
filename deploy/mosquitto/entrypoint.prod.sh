#!/bin/sh
# Copyright (c) 2026 Vassel Goleyu. MIT License.
# Builds the password file and config from the environment, then runs the broker.
set -eu
# A restarted container keeps its filesystem, and mosquitto_passwd -c refuses to overwrite: without this
# rm every restart (host reboot, OOM kill) leaves the broker in a crash loop.
rm -f /mosquitto/passwd
mosquitto_passwd -b -c /mosquitto/passwd device "$MQTT_DEVICE_PASSWORD"
mosquitto_passwd -b /mosquitto/passwd spillway "$MQTT_SERVICE_PASSWORD"
chown mosquitto:mosquitto /mosquitto/passwd /mosquitto/data
cp /mosquitto/config/mosquitto.prod.conf /tmp/mosquitto.conf
if [ -r /mosquitto/certs/fullchain.pem ] && [ -r /mosquitto/certs/privkey.pem ]; then
  mkdir -p /mosquitto/tls
  install -o mosquitto -g mosquitto -m 600 /mosquitto/certs/fullchain.pem /mosquitto/tls/fullchain.pem
  install -o mosquitto -g mosquitto -m 600 /mosquitto/certs/privkey.pem /mosquitto/tls/privkey.pem
  cat >> /tmp/mosquitto.conf <<'CONF'

listener 8883
cafile /mosquitto/tls/fullchain.pem
certfile /mosquitto/tls/fullchain.pem
keyfile /mosquitto/tls/privkey.pem
tls_version tlsv1.2
CONF
  echo "mosquitto: TLS listener enabled on 8883"
else
  echo "mosquitto: no certificates in /mosquitto/certs, TLS listener disabled"
fi
exec mosquitto -c /tmp/mosquitto.conf
