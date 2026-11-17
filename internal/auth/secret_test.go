package auth

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func newCipher(t *testing.T) *Cipher {
	t.Helper()
	c, err := NewCipher(1, key(1), nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	c := newCipher(t)
	plain := []byte("0123456789abcdef0123456789abcdef")
	blob, err := c.Encrypt(plain, []byte("device-1"))
	if err != nil {
		t.Fatal(err)
	}
	if blob[0] != 1 {
		t.Fatalf("blob must start with the key version, got %d", blob[0])
	}
	if bytes.Contains(blob, plain) {
		t.Fatal("ciphertext contains the plaintext")
	}
	got, err := c.Decrypt(blob, []byte("device-1"))
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("round trip failed: %q, %v", got, err)
	}
}

func TestEncryptUsesFreshNonce(t *testing.T) {
	c := newCipher(t)
	a, _ := c.Encrypt([]byte("same"), nil)
	b, _ := c.Encrypt([]byte("same"), nil)
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of the same plaintext must differ")
	}
}

func TestDecryptFailures(t *testing.T) {
	c := newCipher(t)
	blob, _ := c.Encrypt([]byte("secret"), []byte("aad"))

	flip := func(i int) []byte { b := bytes.Clone(blob); b[i] ^= 1; return b }
	wrongKey, _ := NewCipher(1, key(2), nil)
	otherVersion, _ := NewCipher(9, key(1), nil)

	cases := []struct {
		name string
		c    *Cipher
		blob []byte
		aad  []byte
	}{
		{"wrong key", wrongKey, blob, []byte("aad")},
		{"wrong aad (blob moved to another device)", c, blob, []byte("other")},
		{"tampered ciphertext", c, flip(len(blob) - 1), []byte("aad")},
		{"tampered nonce", c, flip(2), []byte("aad")},
		{"unknown key version", otherVersion, blob, []byte("aad")},
		{"truncated", c, blob[:10], []byte("aad")},
		{"empty", c, nil, []byte("aad")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.c.Decrypt(tc.blob, tc.aad); !errors.Is(err, ErrDecrypt) {
				t.Fatalf("err = %v, want ErrDecrypt", err)
			}
		})
	}
}

func TestKeyRotation(t *testing.T) {
	oldC, _ := NewCipher(1, key(1), nil)
	blob, _ := oldC.Encrypt([]byte("s"), []byte("a"))

	rotated, err := NewCipher(2, key(2), map[byte][]byte{1: key(1)})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := rotated.Decrypt(blob, []byte("a")); err != nil || string(got) != "s" {
		t.Fatalf("old key must still decrypt: %q %v", got, err)
	}
	if !rotated.NeedsRotation(blob) {
		t.Fatal("blob under the old key must need rotation")
	}
	fresh, _ := rotated.Encrypt([]byte("s"), []byte("a"))
	if fresh[0] != 2 || rotated.NeedsRotation(fresh) {
		t.Fatal("new blobs use the current key")
	}
	if !rotated.NeedsRotation(nil) {
		t.Fatal("garbage counts as needing attention")
	}
}

func TestNewCipherValidation(t *testing.T) {
	cases := []struct {
		name string
		ver  byte
		cur  []byte
		old  map[byte][]byte
	}{
		{"short key", 1, []byte("short"), nil},
		{"version zero", 0, key(1), nil},
		{"old key short", 2, key(1), map[byte][]byte{1: []byte("x")}},
		{"old reuses current version", 1, key(1), map[byte][]byte{1: key(2)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewCipher(tc.ver, tc.cur, tc.old); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestGenerateDeviceSecret(t *testing.T) {
	raw, h, err := GenerateDeviceSecret()
	if err != nil {
		t.Fatal(err)
	}
	dec, err := hex.DecodeString(h)
	if err != nil || len(raw) != 32 || !bytes.Equal(dec, raw) || len(h) != 64 {
		t.Fatalf("raw=%d hex=%q", len(raw), h)
	}
	raw2, _, _ := GenerateDeviceSecret()
	if bytes.Equal(raw, raw2) {
		t.Fatal("secrets must be random")
	}
}

func TestSubscriptionToken(t *testing.T) {
	tok, hash, err := GenerateSubscriptionToken()
	if err != nil {
		t.Fatal(err)
	}
	// Telegram deep-link payloads allow [A-Za-z0-9_-] up to 64 characters.
	if len(tok) < 20 || len(tok) > 64 {
		t.Fatalf("token length %d", len(tok))
	}
	for _, r := range tok {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			t.Fatalf("token contains %q", r)
		}
	}
	if !bytes.Equal(hash, HashToken(tok)) || len(hash) != 32 {
		t.Fatal("hash must be SHA-256 of the token")
	}
	tok2, _, _ := GenerateSubscriptionToken()
	if tok == tok2 {
		t.Fatal("tokens must be random")
	}
}
