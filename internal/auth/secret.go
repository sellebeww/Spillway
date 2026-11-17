// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Package auth holds secret handling for devices and subscription links.
// OIDC login and JWT validation arrive in a later phase.
package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
)

// Blob layout: version (1 byte) | nonce (12 bytes) | AES-256-GCM ciphertext+tag.
// The version byte selects the key, so keys can be rotated without a flag day.
const (
	keySize   = 32
	nonceSize = 12
)

// ErrDecrypt is returned for every decryption failure. It deliberately says nothing
// more: wrong key, wrong AAD, tampering and truncation are indistinguishable.
var ErrDecrypt = errors.New("decrypt failed")

// Cipher encrypts with the current key and decrypts with the current or any old key.
type Cipher struct {
	current byte
	keys    map[byte][]byte
}

// NewCipher builds a keyring. Versions must be non-zero and unique; all keys 32 bytes.
func NewCipher(currentVersion byte, current []byte, old map[byte][]byte) (*Cipher, error) {
	if currentVersion == 0 {
		return nil, errors.New("key version must be non-zero")
	}
	if len(current) != keySize {
		return nil, fmt.Errorf("current key must be %d bytes", keySize)
	}
	keys := map[byte][]byte{currentVersion: append([]byte(nil), current...)}
	for v, k := range old {
		if v == 0 || v == currentVersion {
			return nil, errors.New("old key version must be non-zero and differ from the current version")
		}
		if len(k) != keySize {
			return nil, fmt.Errorf("old key must be %d bytes", keySize)
		}
		keys[v] = append([]byte(nil), k...)
	}
	return &Cipher{current: currentVersion, keys: keys}, nil
}

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Encrypt seals plaintext under the current key. aad binds the blob to its context
// (for devices: the device id), so a blob copied to another row does not decrypt.
func (c *Cipher) Encrypt(plaintext, aad []byte) ([]byte, error) {
	g, err := gcm(c.keys[c.current])
	if err != nil {
		return nil, fmt.Errorf("init cipher: %w", err)
	}
	out := make([]byte, 1+nonceSize, 1+nonceSize+len(plaintext)+g.Overhead())
	out[0] = c.current
	if _, err := rand.Read(out[1 : 1+nonceSize]); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return g.Seal(out, out[1:1+nonceSize], plaintext, aad), nil
}

// Decrypt opens a blob produced by Encrypt.
func (c *Cipher) Decrypt(blob, aad []byte) ([]byte, error) {
	if len(blob) < 1+nonceSize+16 {
		return nil, ErrDecrypt
	}
	key, ok := c.keys[blob[0]]
	if !ok {
		return nil, ErrDecrypt
	}
	g, err := gcm(key)
	if err != nil {
		return nil, ErrDecrypt
	}
	plain, err := g.Open(nil, blob[1:1+nonceSize], blob[1+nonceSize:], aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plain, nil
}

// NeedsRotation reports whether blob is not encrypted under the current key.
func (c *Cipher) NeedsRotation(blob []byte) bool { return len(blob) == 0 || blob[0] != c.current }

// GenerateDeviceSecret returns 32 random bytes and their hex form (shown to the operator once).
func GenerateDeviceSecret() (raw []byte, hexString string, err error) {
	raw = make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", fmt.Errorf("generate device secret: %w", err)
	}
	return raw, hex.EncodeToString(raw), nil
}

// GenerateSubscriptionToken returns a random URL-safe token (fits Telegram's 64 character
// deep-link limit) and its SHA-256, the only form that is stored.
func GenerateSubscriptionToken() (token string, hash []byte, err error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

// HashToken returns the SHA-256 of a subscription token.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
