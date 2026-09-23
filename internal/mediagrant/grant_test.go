package mediagrant

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

func testCodec(t *testing.T, audience string) *Codec {
	t.Helper()
	c, err := New("k1", audience, map[string]string{"k1": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func testClaims() Claims {
	return Claims{Version: 1, Session: Session("session-one"), LinkID: 42, AssetID: 5, SourceCID: "immutable-manifest", Mode: "blur_faces", Purpose: "gallery", Operations: []string{"image"}, IssuedAt: 1_800_000_000, ExpiresAt: 1_800_000_300, Epoch: "epoch", AssetVersion: "0", FileVersion: "0"}
}

func TestEncryptedGrantBoundaries(t *testing.T) {
	c := testCodec(t, "stand-a")
	claim := testClaims()
	now := time.Unix(claim.IssuedAt, 0)
	encoded, err := c.Seal(claim, now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encoded, claim.SourceCID) {
		t.Fatal("grant leaked locator")
	}
	_, body, _ := strings.Cut(encoded, ".")
	raw, _ := base64.RawURLEncoding.DecodeString(body)
	if bytes.Contains(raw, []byte(claim.SourceCID)) {
		t.Fatal("decoded grant leaked locator")
	}
	other, _ := c.Seal(claim, now)
	if other == encoded {
		t.Fatal("nonce reuse")
	}
	got, err := c.Open(encoded, "session-one", now)
	if err != nil || got.SourceCID != claim.SourceCID {
		t.Fatalf("roundtrip: %+v %v", got, err)
	}
	for _, tc := range []struct {
		name, token string
		codec       *Codec
		at          time.Time
		want        error
	}{
		{"other session", "session-two", c, now, ErrInvalid},
		{"missing session", "", c, now, ErrInvalid},
		{"other deployment", "session-one", testCodec(t, "stand-b"), now, ErrInvalid},
		{"exact expiry", "session-one", c, time.Unix(claim.ExpiresAt, 0), ErrExpired},
		{"future issue", "session-one", c, now.Add(-time.Second), ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.codec.Open(encoded, tc.token, tc.at)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
	for i := 0; i < len(raw); i++ {
		corrupt := bytes.Clone(raw)
		corrupt[i] ^= 1
		_, err := c.Open("k1."+base64.RawURLEncoding.EncodeToString(corrupt), "session-one", now)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("tampering byte %d accepted: %v", i, err)
		}
	}
}

func TestGrantCannotEscalateOperationsOrLifetime(t *testing.T) {
	c := testCodec(t, "stand-a")
	now := time.Unix(testClaims().IssuedAt, 0)
	for _, mutate := range []func(*Claims){
		func(c *Claims) { c.Operations = []string{"bundle"} },
		func(c *Claims) { c.Video = true; c.Operations = []string{"stream"} },
		func(c *Claims) { c.ExpiresAt++ },
		func(c *Claims) { c.Purpose = "verification" },
		func(c *Claims) { c.SourceCID = "a/../../b" },
		func(c *Claims) { c.Version = 2 },
	} {
		claim := testClaims()
		mutate(&claim)
		if _, err := c.Seal(claim, now); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted invalid claims: %+v %v", claim, err)
		}
	}
	claim := testClaims()
	claim.Video = true
	claim.Operations = []string{"poster"}
	if _, err := c.Seal(claim, now); err != nil {
		t.Fatal(err)
	}
	claim.Mode = "original"
	claim.Operations = append(claim.Operations, "stream")
	if _, err := c.Seal(claim, now); err != nil {
		t.Fatal(err)
	}
}

func TestRotationRetainsOldKeyOnlyWhileConfigured(t *testing.T) {
	old := testCodec(t, "a")
	claim := testClaims()
	now := time.Unix(claim.IssuedAt, 0)
	encoded, _ := old.Seal(claim, now)
	keys := map[string]string{"k1": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "k2": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))}
	rotated, err := New("k2", "a", keys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rotated.Open(encoded, "session-one", now); err != nil {
		t.Fatal(err)
	}
	delete(keys, "k1")
	retired, _ := New("k2", "a", keys)
	if _, err := retired.Open(encoded, "session-one", now); !errors.Is(err, ErrInvalid) {
		t.Fatal("retired key accepted")
	}
}
