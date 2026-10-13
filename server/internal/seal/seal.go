// Package seal encrypts and authenticates small values (cookies, stored
// refresh tokens) with the server's secret key, using AES-256-GCM.
package seal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrInvalid = errors.New("sealed value is invalid or expired")

type Sealer struct {
	aead cipher.AEAD
}

// New returns a Sealer for a 32-byte key.
func New(key []byte) (*Sealer, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secret key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal encrypts plaintext. purpose binds the result to one use, so a value
// sealed for one purpose cannot be replayed as another.
func (s *Sealer) Seal(purpose string, plaintext []byte) []byte {
	nonce := make([]byte, s.aead.NonceSize(), s.aead.NonceSize()+len(plaintext)+s.aead.Overhead())
	rand.Read(nonce)
	return s.aead.Seal(nonce, nonce, plaintext, []byte(purpose))
}

func (s *Sealer) Open(purpose string, sealed []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n {
		return nil, ErrInvalid
	}
	out, err := s.aead.Open(nil, sealed[:n], sealed[n:], []byte(purpose))
	if err != nil {
		return nil, ErrInvalid
	}
	return out, nil
}

type envelope[T any] struct {
	Exp   int64 `json:"exp"`
	Value T     `json:"v"`
}

// SealJSON seals v with an expiry, as a URL-safe string for cookies.
func SealJSON[T any](s *Sealer, purpose string, v T, ttl time.Duration) (string, error) {
	b, err := json.Marshal(envelope[T]{Exp: time.Now().Add(ttl).Unix(), Value: v})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(s.Seal(purpose, b)), nil
}

// OpenJSON reverses SealJSON, failing for tampered or expired values.
func OpenJSON[T any](s *Sealer, purpose, sealed string) (T, error) {
	var env envelope[T]
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		return env.Value, ErrInvalid
	}
	b, err := s.Open(purpose, raw)
	if err != nil {
		return env.Value, err
	}
	if err := json.Unmarshal(b, &env); err != nil || time.Now().Unix() > env.Exp {
		return env.Value, ErrInvalid
	}
	return env.Value, nil
}
