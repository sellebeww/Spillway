// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Host tests of the portable core. The reference HMAC below is test-only (FIPS 180-4 / RFC 2104);
// it is itself checked against RFC 4231 test case 2 before any golden vector is trusted.
#include <string.h>
#include <unity.h>

#include "spillway_core.h"

using namespace spillway;

namespace {
const uint32_t K[64] = {
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be,
    0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa,
    0x5cb0a9dc, 0x76f988da, 0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967, 0x27b70a85,
    0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3,
    0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070, 0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f,
    0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2};
uint32_t rotr(uint32_t x, int n) { return (x >> n) | (x << (32 - n)); }

void sha256(const uint8_t *msg, size_t len, uint8_t out[32]) {
  uint32_t h[8] = {0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19};
  uint8_t block[128];
  size_t total = ((len + 9 + 63) / 64) * 64;
  for (size_t off = 0; off < total; off += 64) {
    for (size_t i = 0; i < 64; i++) {
      size_t p = off + i;
      uint8_t b = 0;
      if (p < len) b = msg[p];
      else if (p == len) b = 0x80;
      else if (p >= total - 8) b = (uint8_t)(((uint64_t)len * 8) >> (8 * (total - 1 - p)));
      block[i] = b;
    }
    uint32_t w[64];
    for (int i = 0; i < 16; i++) w[i] = (uint32_t)block[4 * i] << 24 | block[4 * i + 1] << 16 | block[4 * i + 2] << 8 | block[4 * i + 3];
    for (int i = 16; i < 64; i++) {
      uint32_t s0 = rotr(w[i - 15], 7) ^ rotr(w[i - 15], 18) ^ (w[i - 15] >> 3);
      uint32_t s1 = rotr(w[i - 2], 17) ^ rotr(w[i - 2], 19) ^ (w[i - 2] >> 10);
      w[i] = w[i - 16] + s0 + w[i - 7] + s1;
    }
    uint32_t a = h[0], b = h[1], c = h[2], d = h[3], e = h[4], f = h[5], g = h[6], hh = h[7];
    for (int i = 0; i < 64; i++) {
      uint32_t t1 = hh + (rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25)) + ((e & f) ^ (~e & g)) + K[i] + w[i];
      uint32_t t2 = (rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22)) + ((a & b) ^ (a & c) ^ (b & c));
      hh = g; g = f; f = e; e = d + t1; d = c; c = b; b = a; a = t1 + t2;
    }
    h[0] += a; h[1] += b; h[2] += c; h[3] += d; h[4] += e; h[5] += f; h[6] += g; h[7] += hh;
  }
  for (int i = 0; i < 8; i++) for (int j = 0; j < 4; j++) out[4 * i + j] = (uint8_t)(h[i] >> (24 - 8 * j));
}

bool refHmac(const uint8_t *key, size_t kl, const uint8_t *msg, size_t ml, uint8_t out[32]) {
  if (kl > 64) return false;  // keys here are at most 32 bytes
  uint8_t buf[64 + 512], inner[32];
  if (ml > 512) return false;
  for (int i = 0; i < 64; i++) buf[i] = (i < (int)kl ? key[i] : 0) ^ 0x36;
  memcpy(buf + 64, msg, ml);
  sha256(buf, 64 + ml, inner);
  for (int i = 0; i < 64; i++) buf[i] = (i < (int)kl ? key[i] : 0) ^ 0x5c;
  memcpy(buf + 64, inner, 32);
  sha256(buf, 96, out);
  return true;
}

uint8_t kSecret[32];
}  // namespace

void setUp() { hexDecode("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", kSecret, 32); }
void tearDown() {}

void test_reference_hmac_matches_rfc4231_case2() {
  uint8_t mac[32];
  char hex[65];
  TEST_ASSERT_TRUE(refHmac((const uint8_t *)"Jefe", 4, (const uint8_t *)"what do ya want for nothing?", 28, mac));
  hexEncode(mac, 32, hex, sizeof hex);
  TEST_ASSERT_EQUAL_STRING("5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843", hex);
}

void test_golden_vectors() { TEST_ASSERT_TRUE(selfTest(refHmac)); }

void test_golden_vector_exact_strings() {
  char buf[320];
  TEST_ASSERT_TRUE(buildPayload(buf, sizeof buf, refHmac, kSecret, "dev-001", "a1b2c3d4", 42, 1767225600, 1234, 3710) > 0);
  TEST_ASSERT_NOT_NULL(strstr(buf, "\"batt_v\":3.71,\"sig\":\"cab92fb9c8fc57c187c24b9c38a48251965dff0bc3c8df030332453bb85afd50\""));
  char c[96];
  TEST_ASSERT_EQUAL(40, canonical(c, sizeof c, "dev-001", "a1b2c3d4", 42, 1767225600, 1234, 3710));
  TEST_ASSERT_EQUAL_STRING("dev-001|a1b2c3d4|42|1767225600|1234|3.71", c);
  canonical(c, sizeof c, "dev-001", "a1b2c3d4", 43, 1767225660, 1230, kNoBattery);
  TEST_ASSERT_EQUAL_STRING("dev-001|a1b2c3d4|43|1767225660|1230|", c);  // trailing separator, empty batt_v
}

void test_battery_always_two_decimals() {
  char c[96];
  canonical(c, sizeof c, "d", "s", 1, 1, 1, 3700);
  TEST_ASSERT_EQUAL_STRING("d|s|1|1|1|3.70", c);
  canonical(c, sizeof c, "d", "s", 1, 1, 1, 4000);
  TEST_ASSERT_EQUAL_STRING("d|s|1|1|1|4.00", c);
  canonical(c, sizeof c, "d", "s", 1, 1, 1, 3305);  // 3.305 truncates to 3.30: never rounds up past what ADC measured
  TEST_ASSERT_EQUAL_STRING("d|s|1|1|1|3.30", c);
  canonical(c, sizeof c, "d", "s", 1, 1, 1, 50);
  TEST_ASSERT_EQUAL_STRING("d|s|1|1|1|0.05", c);
}

void test_max_values_and_no_overflow() {
  char c[160];
  TEST_ASSERT_TRUE(canonical(c, sizeof c, "d", "s", 4294967295u, 4102444800LL, 100000, kNoBattery) > 0);
  TEST_ASSERT_EQUAL_STRING("d|s|4294967295|4102444800|100000|", c);
  char tiny[8];
  TEST_ASSERT_EQUAL(-1, canonical(tiny, sizeof tiny, "dev-001", "a1b2c3d4", 42, 1767225600, 1234, 3710));
  char smallJson[40];
  TEST_ASSERT_EQUAL(-1, buildPayload(smallJson, sizeof smallJson, refHmac, kSecret, "dev-001", "a1b2c3d4", 42, 1767225600, 1234, 3710));
}

static bool failingHmac(const uint8_t *, size_t, const uint8_t *, size_t, uint8_t[32]) { return false; }
void test_hmac_failure_is_reported() {
  char buf[320];
  TEST_ASSERT_EQUAL(-1, buildPayload(buf, sizeof buf, failingHmac, kSecret, "d", "s", 1, 1, 1, kNoBattery));
  TEST_ASSERT_FALSE(selfTest(failingHmac));
}

void test_median() {
  int a[5] = {1200, 1210, 9000, 1205, 30};  // two echo outliers
  TEST_ASSERT_EQUAL(1205, median(a, 5));
  TEST_ASSERT_EQUAL(1200, a[0]);  // input untouched
  int b[3] = {3, 1, 2};
  TEST_ASSERT_EQUAL(2, median(b, 3));
  int one[1] = {7};
  TEST_ASSERT_EQUAL(7, median(one, 1));
  TEST_ASSERT_EQUAL(0, median(one, 0));
}

void test_range() {
  TEST_ASSERT_TRUE(inRange(250, 250, 4500));
  TEST_ASSERT_TRUE(inRange(4500, 250, 4500));
  TEST_ASSERT_FALSE(inRange(249, 250, 4500));
  TEST_ASSERT_FALSE(inRange(4501, 250, 4500));
}

void test_hex() {
  uint8_t b[2];
  TEST_ASSERT_TRUE(hexDecode("00fF", b, 2));
  TEST_ASSERT_EQUAL_UINT8(0xff, b[1]);
  TEST_ASSERT_FALSE(hexDecode("00f", b, 2));
  TEST_ASSERT_FALSE(hexDecode("00fg", b, 2));
  TEST_ASSERT_FALSE(hexDecode(nullptr, b, 2));
  char out[4];
  TEST_ASSERT_FALSE(hexEncode(b, 2, out, 4));  // needs room for the NUL
}

int main() {
  UNITY_BEGIN();
  RUN_TEST(test_reference_hmac_matches_rfc4231_case2);
  RUN_TEST(test_golden_vectors);
  RUN_TEST(test_golden_vector_exact_strings);
  RUN_TEST(test_battery_always_two_decimals);
  RUN_TEST(test_max_values_and_no_overflow);
  RUN_TEST(test_hmac_failure_is_reported);
  RUN_TEST(test_median);
  RUN_TEST(test_range);
  RUN_TEST(test_hex);
  return UNITY_END();
}
