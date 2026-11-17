// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Package protocol parses telemetry payloads and implements their canonical
// signing string and HMAC verification. See docs/PROTOCOL.md.
package protocol

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Limits enforced by Parse.
const (
	MaxPayloadBytes = 512
	maxRawMM        = 100000
	maxTS           = 4102444800 // 2100-01-01T00:00:00Z
	maxBattV        = 20.0
)

// ErrInvalidPayload is wrapped by every Parse failure.
var ErrInvalidPayload = errors.New("invalid payload")

var (
	idRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	sigRe  = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	battRe = regexp.MustCompile(`^[0-9]{1,2}(\.[0-9]{1,2})?$`) // at most 2 decimals, no exponent, no sign
)

// Payload is a validated telemetry message.
type Payload struct {
	Device string
	SID    string
	FC     uint32
	TS     int64 // epoch seconds UTC
	RawMM  int
	BattV  *float64
	Sig    string // 64 hex characters, any case
}

type wire struct {
	Device *string         `json:"device"`
	SID    *string         `json:"sid"`
	FC     *uint32         `json:"fc"`
	TS     *int64          `json:"ts"`
	RawMM  *int            `json:"raw_mm"`
	BattV  json.RawMessage `json:"batt_v"`
	Sig    *string         `json:"sig"`
}

func invalid(reason string) error { return fmt.Errorf("%w: %s", ErrInvalidPayload, reason) }

// Parse strictly validates a JSON payload. It never panics, never accepts unknown
// fields, trailing data, wrong types or out-of-range values, and never echoes input in errors.
func Parse(data []byte) (Payload, error) {
	if len(data) == 0 || len(data) > MaxPayloadBytes {
		return Payload{}, invalid("size out of bounds")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var w wire
	if err := dec.Decode(&w); err != nil {
		return Payload{}, invalid("malformed or mistyped JSON")
	}
	if _, err := dec.Token(); err != io.EOF {
		return Payload{}, invalid("trailing data")
	}
	if w.Device == nil || w.SID == nil || w.FC == nil || w.TS == nil || w.RawMM == nil || w.Sig == nil {
		return Payload{}, invalid("missing required field")
	}
	switch {
	case !idRe.MatchString(*w.Device):
		return Payload{}, invalid("device must match [A-Za-z0-9_-]{1,64}")
	case !idRe.MatchString(*w.SID):
		return Payload{}, invalid("sid must match [A-Za-z0-9_-]{1,64}")
	case *w.TS < 0 || *w.TS > maxTS:
		return Payload{}, invalid("ts out of range")
	case *w.RawMM < 0 || *w.RawMM > maxRawMM:
		return Payload{}, invalid("raw_mm out of range")
	case !sigRe.MatchString(*w.Sig):
		return Payload{}, invalid("sig must be 64 hex characters")
	}
	batt, err := parseBatt(w.BattV)
	if err != nil {
		return Payload{}, err
	}
	return Payload{Device: *w.Device, SID: *w.SID, FC: *w.FC, TS: *w.TS, RawMM: *w.RawMM, BattV: batt, Sig: *w.Sig}, nil
}

func parseBatt(raw json.RawMessage) (*float64, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return nil, nil
	}
	if !battRe.MatchString(s) {
		return nil, invalid("batt_v must be a plain number with at most 2 decimals")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v > maxBattV {
		return nil, invalid("batt_v out of range")
	}
	return &v, nil
}

// Canonical returns the exact string that is signed:
// device|sid|fc|ts|raw_mm|batt_v with decimal integers and batt_v as %.2f (empty if absent).
func Canonical(p Payload) string {
	batt := ""
	if p.BattV != nil {
		batt = strconv.FormatFloat(*p.BattV, 'f', 2, 64)
	}
	return p.Device + "|" + p.SID + "|" + strconv.FormatUint(uint64(p.FC), 10) + "|" +
		strconv.FormatInt(p.TS, 10) + "|" + strconv.Itoa(p.RawMM) + "|" + batt
}

// Sign returns the lowercase hex HMAC-SHA256 of the canonical string.
func Sign(secret []byte, p Payload) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(Canonical(p)))
	return hex.EncodeToString(m.Sum(nil))
}

// Verify checks p.Sig in constant time.
func Verify(secret []byte, p Payload) bool {
	got, err := hex.DecodeString(p.Sig)
	if err != nil || len(got) != sha256.Size {
		return false
	}
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(Canonical(p)))
	return hmac.Equal(m.Sum(nil), got)
}

// IdempotencyKey identifies a message: (device, sid, fc).
func IdempotencyKey(p Payload) string {
	return "ik:" + p.Device + ":" + p.SID + ":" + strconv.FormatUint(uint64(p.FC), 10)
}
