package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sellebeww/spillway/internal/auth/jwttest"
)

func newVerifier(is *jwttest.Issuer) *Verifier {
	return &Verifier{Issuer: is.URL, Audience: is.Audience, JWKSURL: is.JWKSURL()}
}

func TestVerifyAcceptsAValidToken(t *testing.T) {
	is := jwttest.New(t)
	v := newVerifier(is)
	c := is.Claims("user-1", "a@example.org", "dev")
	got, err := v.Verify(t.Context(), is.Token(c))
	if err != nil || got.Subject != "user-1" || got.Email != "a@example.org" || got.Org != "dev" || !got.EmailVerified {
		t.Fatalf("%+v %v", got, err)
	}
	c["aud"] = []string{"account", is.Audience} // Keycloak sends audience arrays
	if _, err := v.Verify(t.Context(), is.Token(c)); err != nil {
		t.Fatalf("audience array: %v", err)
	}
}

func TestVerifyRejectsEverythingElse(t *testing.T) {
	is := jwttest.New(t)
	v := newVerifier(is)
	ctx := context.Background()
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	mut := func(f func(map[string]any)) string {
		c := is.Claims("u", "e@x.org", "dev")
		f(c)
		return is.Token(c)
	}
	valid := is.Token(is.Claims("u", "e@x.org", "dev"))
	parts := strings.Split(valid, ".")
	forged := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"` + is.URL + `","aud":"spillway-api","sub":"admin","exp":9999999999}`))

	// HMAC algorithm confusion: an HS256 token must never be accepted.
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","kid":"k1"}`))
	m := hmac.New(sha256.New, []byte("anything"))
	m.Write([]byte(h + "." + parts[1]))
	hs := h + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
	none := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + parts[1] + "."

	cases := map[string]string{
		"empty":                 "",
		"garbage":               "not-a-token",
		"two parts":             parts[0] + "." + parts[1],
		"expired":               mut(func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }),
		"no exp":                mut(func(c map[string]any) { delete(c, "exp") }),
		"not yet valid":         mut(func(c map[string]any) { c["nbf"] = time.Now().Add(time.Hour).Unix() }),
		"wrong issuer":          mut(func(c map[string]any) { c["iss"] = "https://evil.example" }),
		"wrong audience":        mut(func(c map[string]any) { c["aud"] = "account" }),
		"audience array miss":   mut(func(c map[string]any) { c["aud"] = []string{"a", "b"} }),
		"no subject":            mut(func(c map[string]any) { delete(c, "sub") }),
		"tampered payload":      parts[0] + "." + forged + "." + parts[2],
		"signed by another key": jwttest.SignWith(other, "k1", "RS256", is.Claims("u", "e@x.org", "dev")),
		"unknown kid":           jwttest.SignWith(other, "nope", "RS256", is.Claims("u", "e@x.org", "dev")),
		"alg HS256":             hs,
		"alg none":              none,
		"oversized":             strings.Repeat("a", 9000),
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := v.Verify(ctx, tok)
			if !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("accepted or wrong error: %v", err)
			}
			if err.Error() != ErrInvalidToken.Error() {
				t.Fatalf("the error must not say why: %q", err)
			}
		})
	}
}

func TestJWKSIsCachedAndRefreshedOnRotation(t *testing.T) {
	is := jwttest.New(t)
	now := time.Now()
	v := newVerifier(is)
	v.Now = func() time.Time { return now }
	long := is.Claims("u", "e@x.org", "dev")
	long["exp"] = now.Add(10 * time.Hour).Unix()
	tok := is.Token(long)
	for i := 0; i < 5; i++ {
		if _, err := v.Verify(t.Context(), tok); err != nil {
			t.Fatal(err)
		}
	}
	if is.Fetches != 1 {
		t.Fatalf("JWKS fetched %d times for 5 verifications", is.Fetches)
	}

	is.AddKey(t, "k2")
	rot := is.Sign("k2", is.Claims("u", "e@x.org", "dev"))
	now = now.Add(time.Minute)
	if _, err := v.Verify(t.Context(), rot); err != nil {
		t.Fatalf("rotated key: %v", err)
	}
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	before := is.Fetches
	for i := 0; i < 20; i++ {
		_, _ = v.Verify(t.Context(), jwttest.SignWith(other, "bogus", "RS256", is.Claims("u", "e", "d")))
	}
	if is.Fetches-before > 1 {
		t.Fatalf("%d fetches for 20 bogus kids", is.Fetches-before)
	}
	is.Fail = true
	now = now.Add(time.Hour)
	if _, err := v.Verify(t.Context(), tok); err != nil {
		t.Fatalf("stale cache must survive a provider outage: %v", err)
	}
}

func TestVerifyClaimsFields(t *testing.T) {
	is := jwttest.New(t)
	v := newVerifier(is)
	c := is.Claims("sub-9", "", "")
	delete(c, "name")
	c["preferred_username"] = "alice"
	c["email_verified"] = false
	got, err := v.Verify(t.Context(), is.Token(c))
	if err != nil || got.Name != "alice" || got.EmailVerified {
		t.Fatalf("%+v %v", got, err)
	}
}
