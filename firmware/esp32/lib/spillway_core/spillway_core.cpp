// Copyright (c) 2026 Vassel Goleyu. MIT License.

#include "spillway_core.h"

#include <stdio.h>
#include <string.h>

namespace spillway {

static int nibble(char c) {
  if (c >= '0' && c <= '9') return c - '0';
  if (c >= 'a' && c <= 'f') return c - 'a' + 10;
  if (c >= 'A' && c <= 'F') return c - 'A' + 10;
  return -1;
}

bool hexDecode(const char *hex, uint8_t *out, size_t n) {
  if (hex == nullptr || strlen(hex) != 2 * n) return false;
  for (size_t i = 0; i < n; i++) {
    int hi = nibble(hex[2 * i]), lo = nibble(hex[2 * i + 1]);
    if (hi < 0 || lo < 0) return false;
    out[i] = (uint8_t)(hi << 4 | lo);
  }
  return true;
}

bool hexEncode(const uint8_t *in, size_t n, char *out, size_t cap) {
  if (cap < 2 * n + 1) return false;
  static const char d[] = "0123456789abcdef";
  for (size_t i = 0; i < n; i++) {
    out[2 * i] = d[in[i] >> 4];
    out[2 * i + 1] = d[in[i] & 15];
  }
  out[2 * n] = '\0';
  return true;
}

int median(const int *values, size_t n) {
  int tmp[15];
  if (n == 0 || n > 15) return 0;
  for (size_t i = 0; i < n; i++) tmp[i] = values[i];
  for (size_t i = 1; i < n; i++) {  // insertion sort: n is tiny
    int v = tmp[i];
    size_t j = i;
    while (j > 0 && tmp[j - 1] > v) {
      tmp[j] = tmp[j - 1];
      j--;
    }
    tmp[j] = v;
  }
  return tmp[(n - 1) / 2];
}

bool inRange(int mm, int lo, int hi) { return mm >= lo && mm <= hi; }

int canonical(char *out, size_t cap, const char *device, const char *sid, uint32_t fc, int64_t ts, int raw_mm, int batt_mv) {
  int n;
  if (batt_mv == kNoBattery) {
    n = snprintf(out, cap, "%s|%s|%lu|%lld|%d|", device, sid, (unsigned long)fc, (long long)ts, raw_mm);
  } else {
    // Integer arithmetic: the protocol wants exactly two decimals and floats would add rounding risk.
    n = snprintf(out, cap, "%s|%s|%lu|%lld|%d|%d.%02d", device, sid, (unsigned long)fc, (long long)ts, raw_mm, batt_mv / 1000,
                 (batt_mv % 1000) / 10);
  }
  return (n < 0 || (size_t)n >= cap) ? -1 : n;
}

int buildPayload(char *out, size_t cap, HmacFn hmac, const uint8_t secret[32], const char *device, const char *sid,
                 uint32_t fc, int64_t ts, int raw_mm, int batt_mv) {
  char canon[160];
  int cl = canonical(canon, sizeof canon, device, sid, fc, ts, raw_mm, batt_mv);
  if (cl < 0) return -1;
  uint8_t mac[32];
  char sig[65];
  if (!hmac(secret, 32, (const uint8_t *)canon, (size_t)cl, mac) || !hexEncode(mac, 32, sig, sizeof sig)) return -1;
  int n;
  if (batt_mv == kNoBattery) {
    n = snprintf(out, cap, "{\"device\":\"%s\",\"sid\":\"%s\",\"fc\":%lu,\"ts\":%lld,\"raw_mm\":%d,\"sig\":\"%s\"}", device, sid,
                 (unsigned long)fc, (long long)ts, raw_mm, sig);
  } else {
    n = snprintf(out, cap, "{\"device\":\"%s\",\"sid\":\"%s\",\"fc\":%lu,\"ts\":%lld,\"raw_mm\":%d,\"batt_v\":%d.%02d,\"sig\":\"%s\"}",
                 device, sid, (unsigned long)fc, (long long)ts, raw_mm, batt_mv / 1000, (batt_mv % 1000) / 10, sig);
  }
  return (n < 0 || (size_t)n >= cap) ? -1 : n;
}

bool selfTest(HmacFn hmac) {
  static const char kSecret[] = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f";
  uint8_t secret[32];
  if (!hexDecode(kSecret, secret, 32)) return false;
  char buf[320];
  const char *want1 =
      "{\"device\":\"dev-001\",\"sid\":\"a1b2c3d4\",\"fc\":42,\"ts\":1767225600,\"raw_mm\":1234,\"batt_v\":3.71,\"sig\":"
      "\"cab92fb9c8fc57c187c24b9c38a48251965dff0bc3c8df030332453bb85afd50\"}";
  const char *want2 =
      "{\"device\":\"dev-001\",\"sid\":\"a1b2c3d4\",\"fc\":43,\"ts\":1767225660,\"raw_mm\":1230,\"sig\":"
      "\"a4e7de1645843cf6d462d828fb170c4fc7d655878a4f8252c154dd690d5b775d\"}";
  return buildPayload(buf, sizeof buf, hmac, secret, "dev-001", "a1b2c3d4", 42, 1767225600, 1234, 3710) > 0 && strcmp(buf, want1) == 0 &&
         buildPayload(buf, sizeof buf, hmac, secret, "dev-001", "a1b2c3d4", 43, 1767225660, 1230, kNoBattery) > 0 && strcmp(buf, want2) == 0;
}

}  // namespace spillway
