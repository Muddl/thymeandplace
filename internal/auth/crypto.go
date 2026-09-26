// Package auth implements single-owner admin authentication.
package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

var b64u = base64.RawURLEncoding

// NewToken returns a random 32-byte token (base64url) and its SHA-256 hash.
func NewToken() (string, []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	raw := b64u.EncodeToString(b)
	return raw, HashToken(raw)
}

// HashToken returns the SHA-256 hash of a raw token string.
func HashToken(raw string) []byte {
	h := sha256.Sum256([]byte(raw))
	return h[:]
}

const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
)

// HashPassword returns an Argon2id PHC string.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks pw against an Argon2id PHC string in constant time.
func VerifyPassword(encoded, pw string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("unsupported password hash")
	}
	var version int
	var m uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("unsupported argon2 version")
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, errors.New("bad argon2 parameters")
	}

	// Validate Argon2 parameters to prevent resource exhaustion from tampered hashes.
	if m < 1 || m > 262144 {
		return false, fmt.Errorf("invalid argon2 memory: %d (must be 1-262144 KiB)", m)
	}
	if t < 1 || t > 10 {
		return false, fmt.Errorf("invalid argon2 time: %d (must be 1-10)", t)
	}
	if p < 1 || p > 16 {
		return false, fmt.Errorf("invalid argon2 parallelism: %d (must be 1-16)", p)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	if len(salt) < 8 || len(salt) > 64 {
		return false, fmt.Errorf("invalid salt length: %d (must be 8-64 bytes)", len(salt))
	}

	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	if len(want) < 16 || len(want) > 64 {
		return false, fmt.Errorf("invalid key length: %d (must be 16-64 bytes)", len(want))
	}

	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// ParseSecretKey decodes ADMIN_SECRET_KEY (standard base64, 32 bytes).
func ParseSecretKey(b64 string) ([]byte, error) {
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("ADMIN_SECRET_KEY is not base64: %w", err)
	}
	if len(k) != 32 {
		return nil, fmt.Errorf("ADMIN_SECRET_KEY must decode to 32 bytes, got %d (generate with: openssl rand -base64 32)", len(k))
	}
	return k, nil
}

// Sealer encrypts small secrets with AES-256-GCM. Output: nonce || ciphertext.
type Sealer struct{ aead cipher.AEAD }

// NewSealer creates a Sealer with a 32-byte AES-256 key.
func NewSealer(key []byte) (*Sealer, error) {
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

// Seal encrypts plaintext with a random nonce, returning nonce || ciphertext.
func (s *Sealer) Seal(plain []byte) []byte {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return s.aead.Seal(nonce, nonce, plain, nil)
}

// Open decrypts a sealed value (nonce || ciphertext) and returns the plaintext.
func (s *Sealer) Open(sealed []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n+s.aead.Overhead() {
		return nil, errors.New("sealed value too short")
	}
	return s.aead.Open(nil, sealed[:n], sealed[n:], nil)
}
