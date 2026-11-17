// Copyright (c) 2026 Vassel Goleyu. MIT License.

#include "hmac_mbedtls.h"

#include "mbedtls/md.h"

bool hmacMbedtls(const uint8_t *key, size_t key_len, const uint8_t *msg, size_t msg_len, uint8_t out[32]) {
  const mbedtls_md_info_t *info = mbedtls_md_info_from_type(MBEDTLS_MD_SHA256);
  return info != nullptr && mbedtls_md_hmac(info, key, key_len, msg, msg_len, out) == 0;
}
