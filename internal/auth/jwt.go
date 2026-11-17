// Copyright (c) 2026 Vassel Goleyu. MIT License.

package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ErrInvalidToken is wrapped by every verification failure. Its message never says why a token was
// refused (signature, audience, expiry...), so responses give an attacker nothing to tune against.
var ErrInvalidToken = errors.New("invalid token")

const maxTokenBytes = 8 << 10

// Claims are the verified claims the API uses.
type Claims struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Org           string // organization slug from the "org" claim (Keycloak user attribute mapper)
	Expires       time.Time
}

// Verifier validates RS256 access tokens against the issuer's JWKS, which is cached and refreshed
// when it ages out or an unknown key id shows up (rate limited, so a flood of bogus kids cannot
// turn into a flood of requests to the identity provider).
type Verifier struct {
	Issuer   string
	Audience string
	JWKSURL  string
	HTTP     *http.Client
	Now      func() time.Time // default time.Now
	Leeway   time.Duration    // default 30 s
	TTL      time.Duration    // JWKS cache lifetime, default 10 min

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
	lastTry   time.Time
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type jwtClaims struct {
	Iss           string          `json:"iss"`
	Aud           json.RawMessage `json:"aud"`
	Sub           string          `json:"sub"`
	Exp           *int64          `json:"exp"`
	Nbf           *int64          `json:"nbf"`
	Email         string          `json:"email"`
	EmailVerified bool            `json:"email_verified"`
	Name          string          `json:"name"`
	Preferred     string          `json:"preferred_username"`
	Org           string          `json:"org"`
}

func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func bad(format string, a ...any) error {
	_ = fmt.Sprintf(format, a...) // the reason is for the caller's tests, never for the client
	return ErrInvalidToken
}

// Verify checks signature, issuer, audience and time bounds, and returns the claims.
func (v *Verifier) Verify(ctx context.Context, raw string) (*Claims, error) {
	if len(raw) == 0 || len(raw) > maxTokenBytes {
		return nil, bad("size")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, bad("shape")
	}
	var hdr jwtHeader
	if err := decodeSegment(parts[0], &hdr); err != nil {
		return nil, bad("header")
	}
	if hdr.Alg != "RS256" { // rejects "none" and HMAC key-confusion attacks outright
		return nil, bad("alg")
	}
	key, err := v.keyFor(ctx, hdr.Kid)
	if err != nil {
		return nil, bad("key")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, bad("sig encoding")
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
		return nil, bad("signature")
	}
	var c jwtClaims
	if err := decodeSegment(parts[1], &c); err != nil {
		return nil, bad("claims")
	}
	now := v.now()
	leeway := v.Leeway
	if leeway == 0 {
		leeway = 30 * time.Second
	}
	switch {
	case c.Iss == "" || c.Iss != v.Issuer:
		return nil, bad("iss")
	case !audienceOK(c.Aud, v.Audience):
		return nil, bad("aud")
	case c.Exp == nil || !now.Before(time.Unix(*c.Exp, 0).Add(leeway)):
		return nil, bad("exp")
	case c.Nbf != nil && now.Add(leeway).Before(time.Unix(*c.Nbf, 0)):
		return nil, bad("nbf")
	case c.Sub == "" || len(c.Sub) > 255:
		return nil, bad("sub")
	}
	name := c.Name
	if name == "" {
		name = c.Preferred
	}
	return &Claims{Subject: c.Sub, Email: c.Email, EmailVerified: c.EmailVerified, Name: name, Org: c.Org, Expires: time.Unix(*c.Exp, 0)}, nil
}

func decodeSegment(seg string, out any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func audienceOK(raw json.RawMessage, want string) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == want
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		for _, a := range many {
			if a == want {
				return true
			}
		}
	}
	return false
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (v *Verifier) keyFor(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	ttl := v.TTL
	if ttl == 0 {
		ttl = 10 * time.Minute
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	k, ok := v.keys[kid]
	fresh := now.Sub(v.fetchedAt) < ttl
	if ok && fresh {
		return k, nil
	}
	// Refresh when stale, or when the kid is unknown (key rotation) but at most every 30 s.
	if (!ok || !fresh) && now.Sub(v.lastTry) >= 30*time.Second {
		v.lastTry = now
		if keys, err := v.fetch(ctx); err == nil {
			v.keys, v.fetchedAt = keys, now
			k, ok = keys[kid]
		}
	}
	if !ok { // a stale cached key is still better than none while the provider is unreachable
		return nil, errors.New("unknown key")
	}
	return k, nil
}

func (v *Verifier) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.JWKSURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build jwks request: %w", err)
	}
	c := v.HTTP
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch jwks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks status %d", resp.StatusCode)
	}
	var doc struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode jwks: %w", err)
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") || (k.Alg != "" && k.Alg != "RS256") {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(n) < 256 || len(e) == 0 || len(e) > 4 { // RSA keys below 2048 bits are refused
			continue
		}
		exp := 0
		for _, b := range e {
			exp = exp<<8 | int(b)
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exp}
	}
	if len(keys) == 0 {
		return nil, errors.New("jwks has no usable RSA keys")
	}
	return keys, nil
}
