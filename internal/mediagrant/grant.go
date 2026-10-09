// Package mediagrant implements the media download capability wire contract.
// Keep this file identical in API and file storage; neither service accepts
// plaintext resource locators or tokens minted for a different deployment.
package mediagrant

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

var (
	ErrInvalid = errors.New("invalid media grant")
	ErrExpired = errors.New("media grant expired")
	ErrRevoked = errors.New("media grant revoked")
)

const MaxTTL = time.Hour

// Claims identify an immutable manifest and the exact operations authorized
// by fresh policy evaluation. HLS children must be resolved from that manifest.
type Claims struct {
	Version      int      `json:"v"`
	Session      string   `json:"s"`
	LinkID       int64    `json:"l"`
	AssetID      int64    `json:"a"`
	SourceCID    string   `json:"c"`
	Video        bool     `json:"video,omitempty"`
	PosterCID    string   `json:"poster,omitempty"`
	Mode         string   `json:"m"`
	Purpose      string   `json:"p"`
	Operations   []string `json:"o"`
	IssuedAt     int64    `json:"iat"`
	ExpiresAt    int64    `json:"exp"`
	Epoch        string   `json:"e"`
	AssetVersion string   `json:"av"`
	FileVersion  string   `json:"fv"`
}

type Codec struct {
	active, audience string
	keys             map[string]cipher.AEAD
}

// New validates the entire key ring, including keys retained for rotation.
// Keys are 32 random bytes encoded as standard base64, never passwords.
func New(active, audience string, keys map[string]string) (*Codec, error) {
	if audience == "" || len(audience) > 256 {
		return nil, errors.New("media grant audience is required")
	}
	c := &Codec{active: active, audience: audience, keys: make(map[string]cipher.AEAD)}
	for id, encoded := range keys {
		if id == "" || len(id) > 32 || strings.ContainsAny(id, ". /\\\t\r\n") {
			return nil, errors.New("invalid media grant key ID")
		}
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("media grant key %s must contain 32 bytes", id)
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		c.keys[id], err = cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
	}
	if c.keys[active] == nil {
		return nil, errors.New("active media grant key is missing")
	}
	return c, nil
}

func Session(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (c *Codec) aad(id string) []byte {
	return []byte("iamfree-media-grant:v1:" + c.audience + ":" + id)
}

func (c *Codec) Seal(claims Claims, now time.Time) (string, error) {
	if err := claims.validate(now); err != nil {
		return "", err
	}
	plain, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	aead := c.keys[c.active]
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, plain, c.aad(c.active))
	return c.active + "." + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (c *Codec) Open(encoded, token string, now time.Time) (Claims, error) {
	var claims Claims
	if token == "" || len(encoded) > 4096 {
		return claims, ErrInvalid
	}
	id, body, ok := strings.Cut(encoded, ".")
	if !ok {
		return claims, ErrInvalid
	}
	aead := c.keys[id]
	if aead == nil {
		return claims, ErrInvalid
	}
	sealed, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || len(sealed) < aead.NonceSize()+aead.Overhead() {
		return claims, ErrInvalid
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], c.aad(id))
	if err != nil || json.Unmarshal(plain, &claims) != nil {
		return Claims{}, ErrInvalid
	}
	if subtle.ConstantTimeCompare([]byte(claims.Session), []byte(Session(token))) != 1 {
		return Claims{}, ErrInvalid
	}
	if err := claims.validate(now); err != nil {
		return Claims{}, err
	}
	return claims, nil
}

func (c Claims) Allows(operation string) bool {
	for _, allowed := range c.Operations {
		if allowed == operation {
			return true
		}
	}
	return false
}

func (c Claims) validate(now time.Time) error {
	if c.Version != 1 || c.LinkID <= 0 || c.AssetID <= 0 || len(c.Session) != 64 || c.Epoch == "" || c.AssetVersion == "" || c.FileVersion == "" {
		return ErrInvalid
	}
	if c.SourceCID == "" || len(c.SourceCID) > 256 || strings.ContainsAny(c.SourceCID, "/\\?# \t\r\n") {
		return ErrInvalid
	}
	if c.Mode != "original" && c.Mode != "blur" && c.Mode != "blur_faces" {
		return ErrInvalid
	}
	var ttl time.Duration
	switch c.Purpose {
	case "gallery", "post", "story", "library":
		ttl = 5 * time.Minute
	case "avatar", "cover":
		ttl = MaxTTL
	case "verification", "internal":
		ttl = time.Minute
	default:
		return ErrInvalid
	}
	if c.IssuedAt <= 0 || c.IssuedAt > now.Unix() || c.ExpiresAt <= c.IssuedAt || c.ExpiresAt-c.IssuedAt > int64(ttl/time.Second) {
		return ErrInvalid
	}
	if now.Unix() >= c.ExpiresAt {
		return ErrExpired
	}
	if len(c.Operations) == 0 || len(c.Operations) > 3 {
		return ErrInvalid
	}
	for _, operation := range c.Operations {
		switch operation {
		case "image":
			if c.Video {
				return ErrInvalid
			}
		case "poster":
			if !c.Video {
				return ErrInvalid
			}
		case "stream":
			if !c.Video || c.Mode != "original" {
				return ErrInvalid
			}
		default:
			return ErrInvalid // bundles and arbitrary child CIDs are never capabilities
		}
	}
	return nil
}
