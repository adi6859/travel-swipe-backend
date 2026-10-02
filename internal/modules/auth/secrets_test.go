package auth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateOTPFormat(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for range 200 {
		code, err := GenerateOTP(6)
		require.NoError(t, err)
		require.Len(t, code, 6)
		require.Regexp(t, `^\d{6}$`, code)
		seen[code] = true
	}
	require.Greater(t, len(seen), 190, "codes should be effectively unique")
}

func TestGenerateOTPKeepsLeadingZeros(t *testing.T) {
	t.Parallel()
	sawLeadingZero := false
	for range 2000 {
		code, err := GenerateOTP(4)
		require.NoError(t, err)
		require.Len(t, code, 4)
		if code[0] == '0' {
			sawLeadingZero = true
		}
	}
	require.True(t, sawLeadingZero)
}

func TestHashOTPIsKeyedAndBoundToPhone(t *testing.T) {
	t.Parallel()
	a := NewSecrets(testOTPSecret)
	b := NewSecrets("different-secret-0123456789abcdef0123")

	h := a.HashOTP(testPhone, "123456")
	require.True(t, a.VerifyOTP(h, testPhone, "123456"))
	require.False(t, a.VerifyOTP(h, testPhone, "123457"))
	require.False(t, a.VerifyOTP(h, "+919000000001", "123456"))
	require.NotEqual(t, h, b.HashOTP(testPhone, "123456"))
}

func TestHashDomainsAreSeparated(t *testing.T) {
	t.Parallel()
	s := NewSecrets(testOTPSecret)
	require.NotEqual(t, s.HashOTP("x", ""), s.HashRefreshToken("x"))
}

func TestGenerateRefreshToken(t *testing.T) {
	t.Parallel()
	a, err := GenerateRefreshToken()
	require.NoError(t, err)
	b, err := GenerateRefreshToken()
	require.NoError(t, err)
	require.NotEqual(t, a, b)
	require.Len(t, a, 43) // 32 bytes, unpadded base64url
}
