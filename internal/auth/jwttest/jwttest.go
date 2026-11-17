// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Package jwttest is a fake OIDC identity provider for tests: it owns RSA keys, serves a JWKS and
// signs tokens. It must only be imported from _test.go files.
package jwttest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Issuer is the fake provider.
type Issuer struct {
	URL      string // issuer identifier and base URL
	Audience string
	mu       sync.Mutex
	keys     map[string]*rsa.PrivateKey
	Fail     bool // when set the JWKS endpoint answers 500
	Fetches  int
	srv      *httptest.Server
}

// New starts the provider with one key, kid "k1".
func New(t *testing.T) *Issuer {
	t.Helper()
	is := &Issuer{Audience: "spillway-api", keys: map[string]*rsa.PrivateKey{}}
	is.AddKey(t, "k1")
	is.srv = httptest.NewServer(http.HandlerFunc(is.serve))
	t.Cleanup(is.srv.Close)
	is.URL = is.srv.URL
	return is
}

// JWKSURL is where the verifier should fetch keys.
func (is *Issuer) JWKSURL() string { return is.srv.URL + "/certs" }

// AddKey generates and publishes another key.
func (is *Issuer) AddKey(t *testing.T, kid string) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	is.mu.Lock()
	is.keys[kid] = k
	is.mu.Unlock()
	return k
}

// RemoveKey stops publishing a key.
func (is *Issuer) RemoveKey(kid string) {
	is.mu.Lock()
	delete(is.keys, kid)
	is.mu.Unlock()
}

func (is *Issuer) serve(w http.ResponseWriter, _ *http.Request) {
	is.mu.Lock()
	defer is.mu.Unlock()
	is.Fetches++
	if is.Fail {
		http.Error(w, "down", http.StatusInternalServerError)
		return
	}
	var ks []map[string]string
	for kid, k := range is.keys {
		ks = append(ks, map[string]string{"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes())})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": ks})
}

// Claims builds a valid claim set; mutate it before signing to make invalid tokens.
func (is *Issuer) Claims(sub, email, org string) map[string]any {
	now := time.Now()
	return map[string]any{"iss": is.URL, "aud": is.Audience, "sub": sub, "email": email, "email_verified": true,
		"name": sub, "org": org, "exp": now.Add(time.Hour).Unix(), "iat": now.Unix()}
}

// Sign signs claims with the key kid using RS256.
func (is *Issuer) Sign(kid string, claims map[string]any) string {
	is.mu.Lock()
	k := is.keys[kid]
	is.mu.Unlock()
	return SignWith(k, kid, "RS256", claims)
}

// Token signs claims with key "k1".
func (is *Issuer) Token(claims map[string]any) string { return is.Sign("k1", claims) }

// SignWith signs with an arbitrary key and algorithm label (for negative tests).
func SignWith(k *rsa.PrivateKey, kid, alg string, claims map[string]any) string {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signing := enc(map[string]string{"alg": alg, "kid": kid, "typ": "JWT"}) + "." + enc(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, sum[:])
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}
