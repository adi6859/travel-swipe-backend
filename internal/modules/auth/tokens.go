package auth

import (
	stdErrors "errors"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/adi6859/travel-swipe-backend/pkg/clock"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
)

// AccessClaims are the claims carried by access tokens. sid binds the token to
// a session so revoking the session invalidates the token.
type AccessClaims struct {
	jwt.RegisteredClaims
	SessionID string `json:"sid"`
}

type TokenIssuer struct {
	secret   []byte
	issuer   string
	audience string
	ttl      time.Duration
	clock    clock.Clock
}

func NewTokenIssuer(secret, issuer, audience string, ttl time.Duration, clk clock.Clock) *TokenIssuer {
	return &TokenIssuer{secret: []byte(secret), issuer: issuer, audience: audience, ttl: ttl, clock: clk}
}

func (t *TokenIssuer) Issue(userID, sessionID uuid.UUID, now time.Time) (string, time.Time, error) {
	expiresAt := now.Add(t.ttl)
	claims := AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			Issuer:    t.issuer,
			Audience:  jwt.ClaimStrings{t.audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			ID:        uuid.NewString(),
		},
		SessionID: sessionID.String(),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
	if err != nil {
		return "", time.Time{}, apperrors.Internal("unable to sign access token", err)
	}
	return signed, expiresAt, nil
}

// Parse verifies signature, algorithm, issuer, audience, and expiry.
func (t *TokenIssuer) Parse(token string) (Principal, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(t.issuer),
		jwt.WithAudience(t.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(t.clock.Now),
	)
	var claims AccessClaims
	_, err := parser.ParseWithClaims(token, &claims, func(*jwt.Token) (any, error) { return t.secret, nil })
	if stdErrors.Is(err, jwt.ErrTokenExpired) {
		return Principal{}, apperrors.AccessTokenExpired("access token expired")
	}
	if err != nil {
		return Principal{}, apperrors.AccessTokenInvalid("invalid access token")
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return Principal{}, apperrors.AccessTokenInvalid("invalid access token")
	}
	sessionID, err := uuid.Parse(claims.SessionID)
	if err != nil {
		return Principal{}, apperrors.AccessTokenInvalid("invalid access token")
	}
	return Principal{UserID: userID, SessionID: sessionID}, nil
}
