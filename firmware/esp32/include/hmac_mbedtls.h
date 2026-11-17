#pragma once
// Copyright (c) 2026 Vassel Goleyu. MIT License.
#include <stddef.h>
#include <stdint.h>

// HMAC-SHA256 with mbedtls (hardware accelerated SHA on the ESP32), for spillway::HmacFn.
bool hmacMbedtls(const uint8_t *key, size_t key_len, const uint8_t *msg, size_t msg_len, uint8_t out[32]);
