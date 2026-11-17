// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Portable core of the Spillway firmware: no Arduino, no ESP-IDF, no Wi-Fi. It builds the
// canonical string and the JSON payload exactly as docs/PROTOCOL.md specifies, so the same
// code runs on the ESP32 and in host unit tests (the golden vectors of the server).
// HMAC-SHA256 is injected: the device uses mbedtls (hmac_mbedtls.cpp), tests use a reference.
#pragma once

#include <stddef.h>
#include <stdint.h>

namespace spillway {

// Computes HMAC-SHA256(key, msg) into out[32]. Returns true on success.
typedef bool (*HmacFn)(const uint8_t *key, size_t key_len, const uint8_t *msg, size_t msg_len, uint8_t out[32]);

// Sentinel for "no battery reading".
const int kNoBattery = -1;

// Decodes exactly 2*n hex characters (either case) into out[n]. False on any other input.
bool hexDecode(const char *hex, uint8_t *out, size_t n);

// Writes 2*n lowercase hex characters plus NUL into out (cap must be >= 2*n+1).
bool hexEncode(const uint8_t *in, size_t n, char *out, size_t cap);

// Median of n (1..15) values; the input array is left untouched. Even n returns the lower middle.
int median(const int *values, size_t n);

// True when mm lies inside [lo, hi].
bool inRange(int mm, int lo, int hi);

// Writes "device|sid|fc|ts|raw_mm|batt_v" with batt_v as exactly two decimals ("3.71"), or empty
// when batt_mv == kNoBattery. Returns the length, or -1 when it does not fit in cap.
int canonical(char *out, size_t cap, const char *device, const char *sid, uint32_t fc, int64_t ts, int raw_mm, int batt_mv);

// Builds the signed telemetry JSON for secret (32 raw bytes). Returns the length, or -1 on
// overflow or HMAC failure. batt_v is omitted from the JSON when batt_mv == kNoBattery.
int buildPayload(char *out, size_t cap, HmacFn hmac, const uint8_t secret[32], const char *device, const char *sid,
                 uint32_t fc, int64_t ts, int raw_mm, int batt_mv);

// Checks the two golden vectors of docs/PROTOCOL.md against hmac. Run at boot: a device that
// cannot reproduce them would only send messages the server rejects.
bool selfTest(HmacFn hmac);

}  // namespace spillway
