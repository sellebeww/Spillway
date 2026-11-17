// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Spillway water level node: ultrasonic sensor -> median of 5 -> signed JSON -> MQTT QoS 1.
// Arduino framework (see docs/DECISIONS.md). Not tested on hardware by the author of this commit.
#include <Arduino.h>
#include <WiFi.h>
#include <esp_system.h>
#include <time.h>

#include "config.h"
#include "hmac_mbedtls.h"
#include "mqtt_client.h"
#include "secrets.h"  // copy include/secrets.example.h to include/secrets.h
#include "spillway_core.h"

static uint8_t g_secret[32];
static char g_sid[9];  // random per boot: the server keys idempotency on (device, sid, fc)
static uint32_t g_fc = 0;
static esp_mqtt_client_handle_t g_mqtt = nullptr;
static volatile bool g_mqttUp = false;
static char g_topic[96];

static void halt(const char *why) {
  for (;;) {
    Serial.printf("FATAL: %s\n", why);
    delay(5000);
  }
}

static void wifiConnect() {
  if (WiFi.status() == WL_CONNECTED) return;
  WiFi.mode(WIFI_STA);
  WiFi.begin(WIFI_SSID, WIFI_PASSWORD);
  for (int i = 0; i < 40 && WiFi.status() != WL_CONNECTED; i++) delay(500);
  Serial.printf("wifi: %s\n", WiFi.status() == WL_CONNECTED ? "connected" : "not connected, will retry");
}

static bool timeSynced() { return time(nullptr) >= MIN_VALID_EPOCH; }

static void ntpSync() {
  configTime(0, 0, NTP_SERVER);  // UTC: the protocol timestamp is epoch seconds
  for (int i = 0; i < 40 && !timeSynced(); i++) delay(500);
}

// One ping. Returns the distance in mm, or -1 on timeout. Speed of sound assumes AMBIENT_TEMP_C.
static int pingMm() {
  digitalWrite(PIN_TRIG, LOW);
  delayMicroseconds(4);
  digitalWrite(PIN_TRIG, HIGH);
  delayMicroseconds(10);
  digitalWrite(PIN_TRIG, LOW);
  unsigned long us = pulseIn(PIN_ECHO, HIGH, 30000UL);  // 30 ms is about 5 m
  if (us == 0) return -1;
  float mmPerUs = (331.3f + 0.606f * AMBIENT_TEMP_C) / 1000.0f;  // m/s to mm/us
  return (int)(us * mmPerUs / 2.0f + 0.5f);
}

// Median of SAMPLES pings; false when too few pings were valid or the result is out of range.
static bool readDistanceMm(int *out) {
  int v[SAMPLES], n = 0;
  for (int i = 0; i < SAMPLES; i++) {
    int mm = pingMm();
    if (mm > 0) v[n++] = mm;
    delay(SAMPLE_GAP_MS);
  }
  if (n < MIN_GOOD_SAMPLES) return false;
  *out = spillway::median(v, n);
  return spillway::inRange(*out, SENSOR_MIN_MM, SENSOR_MAX_MM);
}

static int readBatteryMv() {
#if PIN_BATT_ADC >= 0
  return (int)(analogReadMilliVolts(PIN_BATT_ADC) * BATT_DIVIDER);
#else
  return spillway::kNoBattery;
#endif
}

static void onMqtt(void *, esp_event_base_t, int32_t id, void *) {
  if (id == MQTT_EVENT_CONNECTED) g_mqttUp = true;
  if (id == MQTT_EVENT_DISCONNECTED) g_mqttUp = false;
  // MQTT_EVENT_PUBLISHED (the PUBACK) needs no action: esp-mqtt keeps unacknowledged QoS 1
  // messages in its outbox and retransmits them after a reconnect.
}

static void mqttStart() {
  esp_mqtt_client_config_t cfg = {};
  cfg.broker.address.uri = MQTT_URI;
  cfg.credentials.username = MQTT_USERNAME;
  cfg.credentials.authentication.password = MQTT_PASSWORD;
  cfg.credentials.client_id = g_sid;  // unique per boot, avoids the broker kicking an older session
  cfg.session.keepalive = 60;
  cfg.network.reconnect_timeout_ms = 5000;  // automatic reconnect
#ifdef MQTT_CA_PEM
  cfg.broker.verification.certificate = MQTT_CA_PEM;
#endif
  g_mqtt = esp_mqtt_client_init(&cfg);
  esp_mqtt_client_register_event(g_mqtt, MQTT_EVENT_ANY, onMqtt, nullptr);
  esp_mqtt_client_start(g_mqtt);
}

void setup() {
  Serial.begin(115200);
  pinMode(PIN_TRIG, OUTPUT);
  pinMode(PIN_ECHO, INPUT);
  // A node that cannot reproduce the protocol golden vectors would only send rejected data.
  if (!spillway::selfTest(hmacMbedtls)) halt("HMAC self-test failed");
  if (!spillway::hexDecode(DEVICE_SECRET_HEX, g_secret, 32)) halt("DEVICE_SECRET_HEX must be 64 hex characters");
  snprintf(g_sid, sizeof g_sid, "%08x", (unsigned)esp_random());
  snprintf(g_topic, sizeof g_topic, "spillway/v1/%s/telemetry", DEVICE_KEY);
  wifiConnect();
  ntpSync();
  mqttStart();
  Serial.printf("ready: device=%s sid=%s interval=%ds\n", DEVICE_KEY, g_sid, SEND_INTERVAL_S);
}

void loop() {
  static unsigned long last = 0;
  if (last != 0 && millis() - last < (unsigned long)SEND_INTERVAL_S * 1000UL) {
    delay(100);
    return;
  }
  last = millis() ? millis() : 1;

  wifiConnect();
  if (!timeSynced()) {
    ntpSync();
    if (!timeSynced()) {
      Serial.println("skip: clock not synced");
      return;
    }
  }
  int mm;
  if (!readDistanceMm(&mm)) {
    Serial.println("skip: no valid distance");  // fail-visible: no data, the server shows DATA TERTUNDA
    return;
  }
  char json[256];
  int len = spillway::buildPayload(json, sizeof json, hmacMbedtls, g_secret, DEVICE_KEY, g_sid, g_fc, (int64_t)time(nullptr), mm,
                                   readBatteryMv());
  if (len < 0) {
    Serial.println("skip: payload build failed");
    return;
  }
  int id = esp_mqtt_client_publish(g_mqtt, g_topic, json, len, 1, 0);  // QoS 1, not retained
  Serial.printf("fc=%lu raw_mm=%d mqtt=%s msg_id=%d\n", (unsigned long)g_fc, mm, g_mqttUp ? "up" : "down", id);
  if (id >= 0) g_fc++;  // a rejected enqueue does not burn a frame counter
}
