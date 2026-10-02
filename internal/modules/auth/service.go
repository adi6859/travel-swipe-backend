package auth

import (
	"context"
	stdErrors "errors"
	"log/slog"
	"regexp"
	"time"

	"github.com/google/uuid"

	"github.com/adi6859/travel-swipe-backend/internal/modules/users"
	"github.com/adi6859/travel-swipe-backend/internal/platform/database"
	"github.com/adi6859/travel-swipe-backend/internal/platform/logger"
	"github.com/adi6859/travel-swipe-backend/pkg/clock"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
	"github.com/adi6859/travel-swipe-backend/pkg/require"
)

var (
	phoneE164 = regexp.MustCompile(`^\+[1-9]\d{7,14}$`)
	digits    = regexp.MustCompile(`^\d+$`)
)

const (
	otpDailyWindow  = 24 * time.Hour
	maxUserAgentLen = 512
	maxDeviceLen    = 100
)

type Config struct {
	OTPLength         int
	OTPTTL            time.Duration
	OTPResendCooldown time.Duration
	OTPMaxAttempts    int
	OTPMaxPerPhoneDay int
	RefreshTokenTTL   time.Duration
	// TestPhoneOTPs maps fixed phones to fixed codes; no SMS is sent for them.
	TestPhoneOTPs map[string]string
}

type Service struct {
	cfg     Config
	store   Store
	users   Users
	tx      database.TxRunner
	tokens  *TokenIssuer
	secrets *Secrets
	sms     SMSSender
	clock   clock.Clock
	logger  *slog.Logger
}

func NewService(cfg Config, store Store, usersRepo Users, tx database.TxRunner, tokens *TokenIssuer,
	secrets *Secrets, sms SMSSender, clk clock.Clock, log *slog.Logger,
) *Service {
	require.AllNotNil("auth service", map[string]any{
		"store": store, "users": usersRepo, "tx": tx, "tokens": tokens,
		"secrets": secrets, "sms": sms, "clock": clk, "logger": log,
	})
	return &Service{cfg: cfg, store: store, users: usersRepo, tx: tx, tokens: tokens,
		secrets: secrets, sms: sms, clock: clk, logger: log}
}

// RequestOTP issues a login challenge for phone and delivers the code by SMS.
// The same call serves sign-up, login, and resend.
func (s *Service) RequestOTP(ctx context.Context, phone, clientIP string) (OTPRequested, error) {
	if !phoneE164.MatchString(phone) {
		return OTPRequested{}, apperrors.InvalidFields("request validation failed",
			map[string]string{"phone": "must be an E.164 phone number, e.g. +919876543210"})
	}
	now := s.clock.Now()

	var out OTPRequested
	err := s.tx.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.store.LockPhone(ctx, phone); err != nil {
			return apperrors.Internal("lock phone", err)
		}

		user, err := s.users.FindByPhone(ctx, phone)
		switch {
		case err == nil && !user.IsActive():
			return apperrors.New(apperrors.CodeAccountSuspended, "account suspended", apperrors.WithSafeMessage())
		case err != nil && !stdErrors.Is(err, users.ErrNotFound):
			return apperrors.Internal("find user", err)
		}

		latest, err := s.store.LatestChallenge(ctx, phone, PurposeLogin)
		switch {
		case err == nil && now.Before(latest.ResendAfter):
			return apperrors.TooManyRequests("please wait before requesting another otp")
		case err != nil && !stdErrors.Is(err, ErrNotFound):
			return apperrors.Internal("latest challenge", err)
		}

		sent, err := s.store.CountChallengesSince(ctx, phone, PurposeLogin, now.Add(-otpDailyWindow))
		if err != nil {
			return apperrors.Internal("count challenges", err)
		}
		if sent >= s.cfg.OTPMaxPerPhoneDay {
			return apperrors.TooManyRequests("daily otp limit reached, try again later")
		}

		code, isTestPhone := s.cfg.TestPhoneOTPs[phone]
		if !isTestPhone {
			if code, err = GenerateOTP(s.cfg.OTPLength); err != nil {
				return apperrors.Internal("generate otp", err)
			}
		}

		challenge := OTPChallenge{
			ID:          newID(),
			PhoneE164:   phone,
			Purpose:     PurposeLogin,
			CodeHash:    s.secrets.HashOTP(phone, code),
			MaxAttempts: s.cfg.OTPMaxAttempts,
			ExpiresAt:   now.Add(s.cfg.OTPTTL),
			ResendAfter: now.Add(s.cfg.OTPResendCooldown),
			RequestIP:   optional(clientIP, 64),
			CreatedAt:   now,
		}
		if err := s.store.CreateChallenge(ctx, challenge); err != nil {
			return apperrors.Internal("create challenge", err)
		}

		// Sent inside the transaction so a delivery failure leaves no challenge
		// behind and does not start the resend cooldown.
		if !isTestPhone {
			if err := s.sms.SendOTP(ctx, phone, code); err != nil {
				return apperrors.Unavailable("unable to send otp, please try again", err)
			}
		}

		out = OTPRequested{ChallengeID: challenge.ID, ExpiresAt: challenge.ExpiresAt, ResendAfter: challenge.ResendAfter}
		s.logger.InfoContext(ctx, "otp issued", "phone", logger.MaskPhone(phone), "challenge_id", challenge.ID, "test_phone", isTestPhone)
		return nil
	})
	if err != nil {
		return OTPRequested{}, asAppError(err, "unable to request otp")
	}
	return out, nil
}

// VerifyOTP checks the latest challenge for phone and, on success, signs the
// user in (creating the account on first login) and starts a new session.
func (s *Service) VerifyOTP(ctx context.Context, phone, code string, client ClientInfo) (LoginResult, error) {
	fields := map[string]string{}
	if !phoneE164.MatchString(phone) {
		fields["phone"] = "must be an E.164 phone number, e.g. +919876543210"
	}
	if len(code) != s.cfg.OTPLength || !digits.MatchString(code) {
		fields["code"] = "must be a numeric code of the expected length"
	}
	if len(fields) > 0 {
		return LoginResult{}, apperrors.InvalidFields("request validation failed", fields)
	}
	now := s.clock.Now()

	// failure holds rejections whose side effects (attempt counts, consumption)
	// must commit, so they are returned only after the transaction succeeds.
	var (
		result  LoginResult
		failure error
	)
	err := s.tx.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.store.LockPhone(ctx, phone); err != nil {
			return apperrors.Internal("lock phone", err)
		}

		challenge, err := s.store.LatestChallenge(ctx, phone, PurposeLogin)
		if stdErrors.Is(err, ErrNotFound) {
			failure = otpInvalid()
			return nil
		}
		if err != nil {
			return apperrors.Internal("latest challenge", err)
		}

		switch {
		case challenge.ConsumedAt != nil:
			failure = otpInvalid()
			return nil
		case !now.Before(challenge.ExpiresAt):
			failure = apperrors.New(apperrors.CodeOTPExpired, "otp expired, request a new one", apperrors.WithSafeMessage())
			return nil
		case challenge.Attempts >= challenge.MaxAttempts:
			failure = otpAttemptsExceeded()
			return nil
		}

		if !s.secrets.VerifyOTP(challenge.CodeHash, phone, code) {
			if err := s.store.IncrementChallengeAttempts(ctx, challenge.ID); err != nil {
				return apperrors.Internal("increment attempts", err)
			}
			if challenge.Attempts+1 >= challenge.MaxAttempts {
				failure = otpAttemptsExceeded()
			} else {
				failure = otpInvalid()
			}
			return nil
		}

		consumed, err := s.store.ConsumeChallenge(ctx, challenge.ID, now)
		if err != nil {
			return apperrors.Internal("consume challenge", err)
		}
		if !consumed {
			failure = otpInvalid()
			return nil
		}

		user, isNew, err := s.findOrCreateUser(ctx, phone, now)
		if err != nil {
			return err
		}
		if !user.IsActive() {
			failure = apperrors.New(apperrors.CodeAccountSuspended, "account suspended", apperrors.WithSafeMessage())
			return nil
		}
		if err := s.users.RecordLogin(ctx, user.ID, now); err != nil {
			return apperrors.Internal("record login", err)
		}

		tokens, err := s.startSession(ctx, user.ID, client, now)
		if err != nil {
			return err
		}
		result = LoginResult{User: user, IsNewUser: isNew, Tokens: tokens}
		return nil
	})
	if err != nil {
		return LoginResult{}, asAppError(err, "unable to verify otp")
	}
	if failure != nil {
		s.logger.WarnContext(ctx, "otp verification rejected", "phone", logger.MaskPhone(phone), "code", apperrors.CodeOf(failure))
		return LoginResult{}, failure
	}
	s.logger.InfoContext(ctx, "user signed in", "user_id", result.User.ID, "session_id", result.Tokens.SessionID, "new_user", result.IsNewUser)
	return result, nil
}

// Refresh rotates a refresh token. Presenting an already-used token is treated
// as theft: the whole session is revoked.
func (s *Service) Refresh(ctx context.Context, rawToken string) (TokenPair, error) {
	if rawToken == "" {
		return TokenPair{}, apperrors.RefreshTokenInvalid("invalid refresh token")
	}
	now := s.clock.Now()
	hash := s.secrets.HashRefreshToken(rawToken)

	var (
		pair    TokenPair
		failure error
	)
	err := s.tx.RunInTx(ctx, func(ctx context.Context) error {
		token, err := s.store.RefreshTokenForUpdate(ctx, hash)
		if stdErrors.Is(err, ErrNotFound) {
			failure = apperrors.RefreshTokenInvalid("invalid refresh token")
			return nil
		}
		if err != nil {
			return apperrors.Internal("load refresh token", err)
		}

		state, err := s.store.SessionState(ctx, token.SessionID, true)
		if err != nil {
			return apperrors.Internal("load session", err)
		}

		switch {
		case state.RevokedAt != nil:
			failure = apperrors.RefreshTokenInvalid("invalid refresh token")
			return nil
		case token.UsedAt != nil:
			if err := s.store.RevokeSession(ctx, state.SessionID, RevokeRefreshReuse, now); err != nil {
				return apperrors.Internal("revoke session", err)
			}
			s.logger.WarnContext(ctx, "refresh token reuse detected; session revoked",
				"session_id", state.SessionID, "user_id", state.UserID)
			failure = apperrors.RefreshTokenInvalid("invalid refresh token")
			return nil
		case !now.Before(token.ExpiresAt) || !now.Before(state.ExpiresAt):
			failure = apperrors.RefreshTokenInvalid("refresh token expired")
			return nil
		case state.UserDeletedAt != nil:
			failure = apperrors.RefreshTokenInvalid("invalid refresh token")
			return nil
		case state.UserStatus != users.StatusActive:
			if err := s.store.RevokeSession(ctx, state.SessionID, RevokeSuspended, now); err != nil {
				return apperrors.Internal("revoke session", err)
			}
			failure = apperrors.New(apperrors.CodeAccountSuspended, "account suspended", apperrors.WithSafeMessage())
			return nil
		}

		marked, err := s.store.MarkRefreshTokenUsed(ctx, token.ID, now)
		if err != nil {
			return apperrors.Internal("mark refresh token used", err)
		}
		if !marked {
			failure = apperrors.RefreshTokenInvalid("invalid refresh token")
			return nil
		}

		expiresAt := now.Add(s.cfg.RefreshTokenTTL)
		if err := s.store.ExtendSession(ctx, state.SessionID, now, expiresAt); err != nil {
			return apperrors.Internal("extend session", err)
		}
		pair, err = s.issueTokens(ctx, state.UserID, state.SessionID, now, expiresAt)
		return err
	})
	if err != nil {
		return TokenPair{}, asAppError(err, "unable to refresh token")
	}
	if failure != nil {
		return TokenPair{}, failure
	}
	return pair, nil
}

// Logout revokes the caller's current session. It is idempotent.
func (s *Service) Logout(ctx context.Context, p Principal) error {
	if err := s.store.RevokeSession(ctx, p.SessionID, RevokeLogout, s.clock.Now()); err != nil {
		return apperrors.Internal("revoke session", err)
	}
	s.logger.InfoContext(ctx, "session logged out", "session_id", p.SessionID)
	return nil
}

// LogoutAll revokes every active session of the caller.
func (s *Service) LogoutAll(ctx context.Context, p Principal) (int, error) {
	n, err := s.store.RevokeUserSessions(ctx, p.UserID, RevokeLogoutAll, s.clock.Now())
	if err != nil {
		return 0, apperrors.Internal("revoke sessions", err)
	}
	s.logger.InfoContext(ctx, "all sessions logged out", "user_id", p.UserID, "revoked", n)
	return n, nil
}

// Authenticate validates an access token and confirms its session is still live.
func (s *Service) Authenticate(ctx context.Context, accessToken string) (Principal, error) {
	p, err := s.tokens.Parse(accessToken)
	if err != nil {
		return Principal{}, err
	}
	state, err := s.store.SessionState(ctx, p.SessionID, false)
	if stdErrors.Is(err, ErrNotFound) {
		return Principal{}, sessionRevoked()
	}
	if err != nil {
		return Principal{}, apperrors.Internal("load session", err)
	}
	if state.UserID != p.UserID {
		return Principal{}, apperrors.AccessTokenInvalid("invalid access token")
	}
	if state.RevokedAt != nil || !s.clock.Now().Before(state.ExpiresAt) || state.UserDeletedAt != nil {
		return Principal{}, sessionRevoked()
	}
	if state.UserStatus != users.StatusActive {
		return Principal{}, apperrors.New(apperrors.CodeAccountSuspended, "account suspended", apperrors.WithSafeMessage())
	}
	return p, nil
}

func (s *Service) findOrCreateUser(ctx context.Context, phone string, now time.Time) (users.User, bool, error) {
	user, err := s.users.FindByPhone(ctx, phone)
	if err == nil {
		return user, false, nil
	}
	if !stdErrors.Is(err, users.ErrNotFound) {
		return users.User{}, false, apperrors.Internal("find user", err)
	}
	user = users.User{
		ID:              newID(),
		PhoneE164:       phone,
		Status:          users.StatusActive,
		PhoneVerifiedAt: now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.users.Create(ctx, user); err != nil {
		return users.User{}, false, apperrors.Internal("create user", err)
	}
	return user, true, nil
}

func (s *Service) startSession(ctx context.Context, userID uuid.UUID, client ClientInfo, now time.Time) (TokenPair, error) {
	expiresAt := now.Add(s.cfg.RefreshTokenTTL)
	session := Session{
		ID:         newID(),
		UserID:     userID,
		DeviceName: optional(client.DeviceName, maxDeviceLen),
		UserAgent:  optional(client.UserAgent, maxUserAgentLen),
		IPAddress:  optional(client.IP, 64),
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  expiresAt,
	}
	if err := s.store.CreateSession(ctx, session); err != nil {
		return TokenPair{}, apperrors.Internal("create session", err)
	}
	return s.issueTokens(ctx, userID, session.ID, now, expiresAt)
}

func (s *Service) issueTokens(ctx context.Context, userID, sessionID uuid.UUID, now, refreshExpiresAt time.Time) (TokenPair, error) {
	raw, err := GenerateRefreshToken()
	if err != nil {
		return TokenPair{}, apperrors.Internal("generate refresh token", err)
	}
	if err := s.store.CreateRefreshToken(ctx, RefreshToken{
		ID:        newID(),
		SessionID: sessionID,
		TokenHash: s.secrets.HashRefreshToken(raw),
		ExpiresAt: refreshExpiresAt,
		CreatedAt: now,
	}); err != nil {
		return TokenPair{}, apperrors.Internal("create refresh token", err)
	}
	access, accessExp, err := s.tokens.Issue(userID, sessionID, now)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{
		SessionID:        sessionID,
		AccessToken:      access,
		AccessExpiresAt:  accessExp,
		RefreshToken:     raw,
		RefreshExpiresAt: refreshExpiresAt,
	}, nil
}

func otpInvalid() error {
	return apperrors.New(apperrors.CodeOTPInvalid, "invalid otp", apperrors.WithSafeMessage())
}

func otpAttemptsExceeded() error {
	return apperrors.New(apperrors.CodeOTPAttemptsExceeded, "too many incorrect attempts, request a new otp", apperrors.WithSafeMessage())
}

func sessionRevoked() error {
	return apperrors.New(apperrors.CodeSessionRevoked, "session is no longer valid, please sign in again", apperrors.WithSafeMessage())
}

func asAppError(err error, msg string) error {
	var appErr *apperrors.AppError
	if stdErrors.As(err, &appErr) {
		return err
	}
	return apperrors.Internal(msg, err)
}

func newID() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}

func optional(v string, maxLen int) *string {
	if v == "" {
		return nil
	}
	if len(v) > maxLen {
		v = v[:maxLen]
	}
	return &v
}
