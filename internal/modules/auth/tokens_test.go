package auth

import (
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/adi6859/travel-swipe-backend/pkg/clock"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
)

func newIssuer(clk clock.Clock) *TokenIssuer {
	return NewTokenIssuer(testJWTSecret, "test-iss", "test-aud", 15*time.Minute, clk)
}

func TestTokenRoundTrip(t *testing.T) {
	t.Parallel()
	clk := &clock.MockClock{Current: time.Now().UTC()}
	iss := newIssuer(clk)
	userID, sessionID := uuid.New(), uuid.New()

	tok, exp, err := iss.Issue(userID, sessionID, clk.Now())
	require.NoError(t, err)
	require.Equal(t, clk.Now().Add(15*time.Minute), exp)

	p, err := iss.Parse(tok)
	require.NoError(t, err)
	require.Equal(t, Principal{UserID: userID, SessionID: sessionID}, p)
}

func TestTokenExpired(t *testing.T) {
	t.Parallel()
	clk := &clock.MockClock{Current: time.Now().UTC()}
	iss := newIssuer(clk)
	tok, _, err := iss.Issue(uuid.New(), uuid.New(), clk.Now())
	require.NoError(t, err)

	clk.Advance(15*time.Minute + time.Second)
	_, err = iss.Parse(tok)
	require.Equal(t, apperrors.CodeAccessTokenExpired, apperrors.CodeOf(err))
}

func TestTokenRejectsWrongSecretIssuerAudience(t *testing.T) {
	t.Parallel()
	clk := &clock.MockClock{Current: time.Now().UTC()}
	tok, _, err := newIssuer(clk).Issue(uuid.New(), uuid.New(), clk.Now())
	require.NoError(t, err)

	for name, other := range map[string]*TokenIssuer{
		"secret":   NewTokenIssuer("another-secret-0123456789abcdef0123", "test-iss", "test-aud", time.Minute, clk),
		"issuer":   NewTokenIssuer(testJWTSecret, "other-iss", "test-aud", time.Minute, clk),
		"audience": NewTokenIssuer(testJWTSecret, "test-iss", "other-aud", time.Minute, clk),
	} {
		_, err := other.Parse(tok)
		require.Equal(t, apperrors.CodeAccessTokenInvalid, apperrors.CodeOf(err), name)
	}
}

func TestTokenRejectsAlgNone(t *testing.T) {
	t.Parallel()
	clk := &clock.MockClock{Current: time.Now().UTC()}
	claims := AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   uuid.NewString(),
			Issuer:    "test-iss",
			Audience:  jwt.ClaimStrings{"test-aud"},
			ExpiresAt: jwt.NewNumericDate(clk.Now().Add(time.Hour)),
		},
		SessionID: uuid.NewString(),
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = newIssuer(clk).Parse(tok)
	require.Equal(t, apperrors.CodeAccessTokenInvalid, apperrors.CodeOf(err))
}

func TestTokenRejectsMissingSessionOrExpiry(t *testing.T) {
	t.Parallel()
	clk := &clock.MockClock{Current: time.Now().UTC()}
	sign := func(c AccessClaims) string {
		s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(testJWTSecret))
		require.NoError(t, err)
		return s
	}
	base := jwt.RegisteredClaims{
		Subject:   uuid.NewString(),
		Issuer:    "test-iss",
		Audience:  jwt.ClaimStrings{"test-aud"},
		ExpiresAt: jwt.NewNumericDate(clk.Now().Add(time.Hour)),
	}

	_, err := newIssuer(clk).Parse(sign(AccessClaims{RegisteredClaims: base}))
	require.Equal(t, apperrors.CodeAccessTokenInvalid, apperrors.CodeOf(err), "missing sid")

	noExp := base
	noExp.ExpiresAt = nil
	_, err = newIssuer(clk).Parse(sign(AccessClaims{RegisteredClaims: noExp, SessionID: uuid.NewString()}))
	require.Equal(t, apperrors.CodeAccessTokenInvalid, apperrors.CodeOf(err), "missing exp")
}
