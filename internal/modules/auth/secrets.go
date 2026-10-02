package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
)

const refreshTokenBytes = 32

// Secrets hashes OTP codes and refresh tokens with a keyed HMAC so a database
// leak alone does not allow offline guessing of short OTPs.
type Secrets struct {
	key []byte
}

func NewSecrets(key string) *Secrets {
	return &Secrets{key: []byte(key)}
}

func (s *Secrets) mac(domain string, parts ...string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(domain))
	for _, p := range parts {
		m.Write([]byte{0})
		m.Write([]byte(p))
	}
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Secrets) HashOTP(phoneE164, code string) string {
	return s.mac("otp", phoneE164, code)
}

// VerifyOTP compares in constant time.
func (s *Secrets) VerifyOTP(storedHash, phoneE164, code string) bool {
	return hmac.Equal([]byte(storedHash), []byte(s.HashOTP(phoneE164, code)))
}

func (s *Secrets) HashRefreshToken(raw string) string {
	return s.mac("refresh", raw)
}

// GenerateOTP returns a uniformly distributed numeric code of the given length.
func GenerateOTP(length int) (string, error) {
	max := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(length)), nil)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", length, n), nil
}

// GenerateRefreshToken returns 256 bits of randomness, base64url-encoded.
func GenerateRefreshToken() (string, error) {
	b := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
