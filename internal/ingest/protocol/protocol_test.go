package protocol

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// Golden vectors: also published in docs/PROTOCOL.md and used by firmware tests.
// Signatures were cross-checked with:
//
//	printf '%s' "<canonical>" | openssl dgst -sha256 -mac HMAC -macopt hexkey:<secret>
const (
	goldenSecretHex = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

	golden1JSON      = `{"device":"dev-001","sid":"a1b2c3d4","fc":42,"ts":1767225600,"raw_mm":1234,"batt_v":3.71,"sig":"cab92fb9c8fc57c187c24b9c38a48251965dff0bc3c8df030332453bb85afd50"}`
	golden1Canonical = "dev-001|a1b2c3d4|42|1767225600|1234|3.71"
	golden1Sig       = "cab92fb9c8fc57c187c24b9c38a48251965dff0bc3c8df030332453bb85afd50"

	golden2JSON      = `{"device":"dev-001","sid":"a1b2c3d4","fc":43,"ts":1767225660,"raw_mm":1230,"sig":"a4e7de1645843cf6d462d828fb170c4fc7d655878a4f8252c154dd690d5b775d"}`
	golden2Canonical = "dev-001|a1b2c3d4|43|1767225660|1230|"
	golden2Sig       = "a4e7de1645843cf6d462d828fb170c4fc7d655878a4f8252c154dd690d5b775d"
)

func secret(t testing.TB) []byte {
	t.Helper()
	b, err := hex.DecodeString(goldenSecretHex)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGoldenVectors(t *testing.T) {
	for _, g := range []struct{ name, json, canonical, sig string }{
		{"with battery", golden1JSON, golden1Canonical, golden1Sig},
		{"without battery", golden2JSON, golden2Canonical, golden2Sig},
	} {
		t.Run(g.name, func(t *testing.T) {
			p, err := Parse([]byte(g.json))
			if err != nil {
				t.Fatal(err)
			}
			if got := Canonical(p); got != g.canonical {
				t.Fatalf("canonical = %q, want %q", got, g.canonical)
			}
			if got := Sign(secret(t), p); got != g.sig {
				t.Fatalf("sig = %s, want %s", got, g.sig)
			}
			if !Verify(secret(t), p) {
				t.Fatal("golden vector must verify")
			}
		})
	}
}

func TestBatteryNullEqualsAbsent(t *testing.T) {
	p, err := Parse([]byte(strings.Replace(golden2JSON, `"raw_mm":1230,`, `"raw_mm":1230,"batt_v":null,`, 1)))
	if err != nil || p.BattV != nil || !Verify(secret(t), p) {
		t.Fatalf("p=%+v err=%v", p, err)
	}
}

func TestBatteryCanonicalFormatting(t *testing.T) {
	cases := map[string]string{"3.7": "3.70", "4": "4.00", "3.71": "3.71", "0.05": "0.05", "12.5": "12.50"}
	for in, want := range cases {
		body := `{"device":"d1","sid":"s1","fc":1,"ts":1767225600,"raw_mm":1000,"batt_v":` + in + `,"sig":"` + strings.Repeat("0", 64) + `"}`
		p, err := Parse([]byte(body))
		if err != nil {
			t.Fatalf("batt %s: %v", in, err)
		}
		if got := Canonical(p); !strings.HasSuffix(got, "|"+want) {
			t.Errorf("batt %s: canonical %q, want suffix %q", in, got, want)
		}
	}
}

// Changing any single field must break verification.
func TestVerifyFailsWhenAnyFieldChanges(t *testing.T) {
	base, err := Parse([]byte(golden1JSON))
	if err != nil {
		t.Fatal(err)
	}
	other := 3.72
	mutations := map[string]func(p *Payload){
		"device":       func(p *Payload) { p.Device = "dev-002" },
		"sid":          func(p *Payload) { p.SID = "a1b2c3d5" },
		"fc":           func(p *Payload) { p.FC++ },
		"ts":           func(p *Payload) { p.TS++ },
		"raw_mm":       func(p *Payload) { p.RawMM++ },
		"batt_v value": func(p *Payload) { p.BattV = &other },
		"batt_v drop":  func(p *Payload) { p.BattV = nil },
		"sig first":    func(p *Payload) { p.Sig = "0" + p.Sig[1:] },
		"sig last":     func(p *Payload) { p.Sig = p.Sig[:63] + "1" }, // golden sig1 already ends in 0
		"sig empty":    func(p *Payload) { p.Sig = "" },
		"sig not hex":  func(p *Payload) { p.Sig = strings.Repeat("z", 64) },
		"sig short":    func(p *Payload) { p.Sig = p.Sig[:62] },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			p := base
			if base.BattV != nil {
				v := *base.BattV
				p.BattV = &v
			}
			mutate(&p)
			if Verify(secret(t), p) {
				t.Fatal("verification passed after mutation")
			}
		})
	}
	t.Run("wrong secret", func(t *testing.T) {
		wrong := secret(t)
		wrong[0] ^= 1
		if Verify(wrong, base) {
			t.Fatal("verified with wrong secret")
		}
	})
	t.Run("uppercase hex sig is accepted", func(t *testing.T) {
		p := base
		p.Sig = strings.ToUpper(p.Sig)
		if !Verify(secret(t), p) {
			t.Fatal("hex is case-insensitive")
		}
	})
	t.Run("adding battery to a battery-less message fails", func(t *testing.T) {
		p, _ := Parse([]byte(golden2JSON))
		v := 3.7
		p.BattV = &v
		if Verify(secret(t), p) {
			t.Fatal("verified")
		}
	})
}

func TestIdempotencyKey(t *testing.T) {
	p, _ := Parse([]byte(golden1JSON))
	if got := IdempotencyKey(p); got != "ik:dev-001:a1b2c3d4:42" {
		t.Fatalf("got %q", got)
	}
	q := p
	q.FC++
	if IdempotencyKey(p) == IdempotencyKey(q) {
		t.Fatal("different fc must give different keys")
	}
}

func TestParseRejectsInvalid(t *testing.T) {
	sig := strings.Repeat("a", 64)
	// with builds a payload with deterministic field order, overriding or dropping one field.
	with := func(field, value string) string {
		order := []string{"device", "sid", "fc", "ts", "raw_mm", "sig"}
		fields := map[string]string{
			"device": `"dev-001"`, "sid": `"sid1"`, "fc": `1`, "ts": `1767225600`, "raw_mm": `1000`, "sig": `"` + sig + `"`,
		}
		if value == "" {
			delete(fields, field)
		} else {
			fields[field] = value
		}
		var parts []string
		for _, k := range order {
			if v, ok := fields[k]; ok {
				parts = append(parts, `"`+k+`":`+v)
			}
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	withBatt := func(b string) string { return strings.Replace(with("fc", "1"), "{", `{"batt_v":`+b+`,`, 1) }
	cases := []struct{ name, body string }{
		{"empty", ``},
		{"not json", `hello`},
		{"array", `[]`},
		{"null", `null`},
		{"empty object", `{}`},
		{"trailing garbage", with("fc", "1") + `x`},
		{"second object", with("fc", "1") + with("fc", "2")},
		{"unknown field", strings.Replace(with("fc", "1"), "{", `{"extra":1,`, 1)},
		{"missing device", with("device", "")},
		{"missing sid", with("sid", "")},
		{"missing fc", with("fc", "")},
		{"missing ts", with("ts", "")},
		{"missing raw_mm", with("raw_mm", "")},
		{"missing sig", with("sig", "")},
		{"null fc", with("fc", "null")},
		{"fc as string", with("fc", `"1"`)},
		{"fc float", with("fc", "1.5")},
		{"fc exponent", with("fc", "1e2")},
		{"fc negative", with("fc", "-1")},
		{"fc above uint32", with("fc", "4294967296")},
		{"ts negative", with("ts", "-1")},
		{"ts beyond year 2100", with("ts", "4102444801")},
		{"ts float", with("ts", "1767225600.5")},
		{"raw negative", with("raw_mm", "-1")},
		{"raw too large", with("raw_mm", "100001")},
		{"raw float", with("raw_mm", "10.5")},
		{"device with pipe", with("device", `"a|b"`)},
		{"device with space", with("device", `"a b"`)},
		{"device empty", with("device", `""`)},
		{"device too long", with("device", `"`+strings.Repeat("a", 65)+`"`)},
		{"device not a string", with("device", `5`)},
		{"sid with pipe", with("sid", `"a|b"`)},
		{"sid unicode", with("sid", `"sidé"`)},
		{"sig too short", with("sig", `"abcd"`)},
		{"sig too long", with("sig", `"`+strings.Repeat("a", 66)+`"`)},
		{"sig not hex", with("sig", `"`+strings.Repeat("g", 64)+`"`)},
		{"batt as string", withBatt(`"3.7"`)},
		{"batt three decimals", withBatt(`3.714`)},
		{"batt exponent", withBatt(`3e0`)},
		{"batt negative", withBatt(`-3.7`)},
		{"batt above 20 V", withBatt(`20.01`)},
		{"oversize", `{"device":"` + strings.Repeat("a", 600) + `"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Parse([]byte(c.body)); !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("err = %v, want ErrInvalidPayload", err)
			}
		})
	}
}

func TestParseAcceptsBoundaries(t *testing.T) {
	sig := strings.Repeat("a", 64)
	body := `{"device":"` + strings.Repeat("A", 64) + `","sid":"` + strings.Repeat("_", 64) + `","fc":4294967295,"ts":4102444800,"raw_mm":100000,"batt_v":20,"sig":"` + sig + `"}`
	p, err := Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if p.FC != 4294967295 || p.TS != 4102444800 || p.RawMM != 100000 {
		t.Fatalf("%+v", p)
	}
	zero := `{"device":"d","sid":"s","fc":0,"ts":0,"raw_mm":0,"sig":"` + sig + `"}`
	if _, err := Parse([]byte(zero)); err != nil {
		t.Fatalf("zero values are legal: %v", err)
	}
}

func TestErrorsNeverEchoSignature(t *testing.T) {
	sig := strings.Repeat("b", 64)
	_, err := Parse([]byte(`{"device":"a|b","sid":"s","fc":1,"ts":1,"raw_mm":1,"sig":"` + sig + `"}`))
	if err == nil || strings.Contains(err.Error(), sig) {
		t.Fatalf("error must not contain the signature: %v", err)
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{
		golden1JSON, golden2JSON, ``, `{}`, `null`, `[1]`, `{"device":`, `{"fc":-1}`,
		`{"device":"a","sid":"b","fc":1,"ts":1,"raw_mm":1,"batt_v":1e999,"sig":"` + strings.Repeat("a", 64) + `"}`,
		"\x00\xff{\"device\"",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := Parse(data) // must never panic
		if err != nil {
			if !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("non-sentinel error: %v", err)
			}
			return
		}
		c := Canonical(p)
		if strings.Count(c, "|") != 5 || len(data) > MaxPayloadBytes {
			t.Fatalf("accepted payload breaks invariants: canonical=%q len=%d", c, len(data))
		}
		if len(p.Sig) != 64 {
			t.Fatalf("sig length %d", len(p.Sig))
		}
		_ = Verify([]byte("k"), p) // must not panic either
	})
}
