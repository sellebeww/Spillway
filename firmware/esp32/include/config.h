// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Tunables that are not secret. Pins are for a common ESP32 DevKit (30/38 pin).
#pragma once

#define SEND_INTERVAL_S 30  // must match the device's expected interval in the admin UI

// Ultrasonic sensor (HC-SR04 or JSN-SR04T in mode 0). ECHO is 5 V on the HC-SR04: use a divider.
#define PIN_TRIG 5
#define PIN_ECHO 18
#define SAMPLES 5            // median of this many pings (odd, 3..15)
#define SAMPLE_GAP_MS 70     // let echoes die out between pings
#define MIN_GOOD_SAMPLES 3   // fewer valid pings than this: send nothing
#define SENSOR_MIN_MM 250    // inside the JSN-SR04T blind zone readings are garbage
#define SENSOR_MAX_MM 4500
#define AMBIENT_TEMP_C 28.0f  // speed of sound assumes this air temperature (see README, accuracy)

// Battery: ADC pin behind a 1:2 resistor divider, or -1 to send no batt_v.
#define PIN_BATT_ADC -1
#define BATT_DIVIDER 2.0f

#define NTP_SERVER "pool.ntp.org"
#define MIN_VALID_EPOCH 1767225600  // 2026-01-01: a clock earlier than this is not synced
