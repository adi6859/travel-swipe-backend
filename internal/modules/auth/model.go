// Package auth implements phone-OTP authentication, sessions, and rotating
// refresh tokens.
package auth

import (
	"context"
	stdErrors "errors"
	"time"

	"github.com/google/uuid"

	"github.com/adi6859/travel-swipe-backend/internal/modules/users"
)

var ErrNotFound = stdErrors.New("auth: not found")

const PurposeLogin = "login"

type RevokeReason string

const (
	RevokeLogout       RevokeReason = "logout"
	RevokeLogoutAll    RevokeReason = "logout_all"
	RevokeRefreshReuse RevokeReason = "refresh_reuse"
	RevokeSuspended    RevokeReason = "suspended"
)

type OTPChallenge struct {
	ID          uuid.UUID  `db:"id"`
	PhoneE164   string     `db:"phone_e164"`
	Purpose     string     `db:"purpose"`
	CodeHash    string     `db:"code_hash"`
	Attempts    int        `db:"attempts"`
	MaxAttempts int        `db:"max_attempts"`
	ExpiresAt   time.Time  `db:"expires_at"`
	ResendAfter time.Time  `db:"resend_after"`
	ConsumedAt  *time.Time `db:"consumed_at"`
	RequestIP   *string    `db:"-"`
	CreatedAt   time.Time  `db:"created_at"`
}

type Session struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	DeviceName *string
	UserAgent  *string
	IPAddress  *string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// SessionState is a session joined with the owning user's status, used to
// authorize requests and refreshes.
type SessionState struct {
	SessionID     uuid.UUID    `db:"id"`
	UserID        uuid.UUID    `db:"user_id"`
	ExpiresAt     time.Time    `db:"expires_at"`
	RevokedAt     *time.Time   `db:"revoked_at"`
	UserStatus    users.Status `db:"user_status"`
	UserDeletedAt *time.Time   `db:"user_deleted_at"`
}

type RefreshToken struct {
	ID        uuid.UUID  `db:"id"`
	SessionID uuid.UUID  `db:"session_id"`
	TokenHash string     `db:"token_hash"`
	ExpiresAt time.Time  `db:"expires_at"`
	UsedAt    *time.Time `db:"used_at"`
	CreatedAt time.Time  `db:"created_at"`
}

// Store persists auth state. All methods join the transaction in ctx when present.
type Store interface {
	// LockPhone serializes OTP operations for one phone until the transaction ends.
	LockPhone(ctx context.Context, phoneE164 string) error
	LatestChallenge(ctx context.Context, phoneE164, purpose string) (OTPChallenge, error)
	CountChallengesSince(ctx context.Context, phoneE164, purpose string, since time.Time) (int, error)
	CreateChallenge(ctx context.Context, c OTPChallenge) error
	IncrementChallengeAttempts(ctx context.Context, id uuid.UUID) error
	// ConsumeChallenge reports false when the challenge was already consumed.
	ConsumeChallenge(ctx context.Context, id uuid.UUID, at time.Time) (bool, error)

	CreateSession(ctx context.Context, s Session) error
	SessionState(ctx context.Context, id uuid.UUID, forUpdate bool) (SessionState, error)
	ExtendSession(ctx context.Context, id uuid.UUID, lastSeen, expiresAt time.Time) error
	RevokeSession(ctx context.Context, id uuid.UUID, reason RevokeReason, at time.Time) error
	RevokeUserSessions(ctx context.Context, userID uuid.UUID, reason RevokeReason, at time.Time) (int, error)

	CreateRefreshToken(ctx context.Context, t RefreshToken) error
	RefreshTokenForUpdate(ctx context.Context, tokenHash string) (RefreshToken, error)
	// MarkRefreshTokenUsed reports false when the token was already used.
	MarkRefreshTokenUsed(ctx context.Context, id uuid.UUID, at time.Time) (bool, error)
}

// Users is the subset of the users module that auth depends on.
type Users interface {
	FindByPhone(ctx context.Context, phoneE164 string) (users.User, error)
	Create(ctx context.Context, u users.User) error
	RecordLogin(ctx context.Context, id uuid.UUID, at time.Time) error
}

// SMSSender delivers OTP codes.
type SMSSender interface {
	SendOTP(ctx context.Context, phoneE164, code string) error
}

// ClientInfo describes the device starting a session.
type ClientInfo struct {
	DeviceName string
	UserAgent  string
	IP         string
}

type OTPRequested struct {
	ChallengeID uuid.UUID
	ExpiresAt   time.Time
	ResendAfter time.Time
}

type TokenPair struct {
	SessionID        uuid.UUID
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
}

type LoginResult struct {
	User      users.User
	IsNewUser bool
	Tokens    TokenPair
}

// Principal identifies the authenticated caller of a request.
type Principal struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
}
