package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/adi6859/travel-swipe-backend/internal/modules/users"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
)

var ctx = context.Background()

func wrongCode(code string) string {
	if code == "000000" {
		return "111111"
	}
	return "000000"
}

// --- RequestOTP ---

func TestRequestOTPSendsCodeAndStoresOnlyHash(t *testing.T) {
	h := newHarness(t)

	out, err := h.svc.RequestOTP(ctx, testPhone, "203.0.113.7")
	require.NoError(t, err)

	code := h.sms.lastCode(t)
	require.Len(t, code, 6)
	stored := h.store.latest()
	require.Equal(t, out.ChallengeID, stored.ID)
	require.NotContains(t, stored.CodeHash, code)
	require.Len(t, stored.CodeHash, 64)
	require.Equal(t, h.clock.Now().Add(h.cfg.OTPTTL), out.ExpiresAt)
	require.Equal(t, h.clock.Now().Add(h.cfg.OTPResendCooldown), out.ResendAfter)
	require.Equal(t, 3, stored.MaxAttempts)
}

func TestRequestOTPRejectsInvalidPhone(t *testing.T) {
	h := newHarness(t)

	_, err := h.svc.RequestOTP(ctx, "9876543210", "")
	require.Equal(t, apperrors.CodeInvalid, apperrors.CodeOf(err))
	require.Zero(t, h.sms.count())
}

func TestRequestOTPEnforcesResendCooldown(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.RequestOTP(ctx, testPhone, "")
	require.NoError(t, err)

	_, err = h.svc.RequestOTP(ctx, testPhone, "")
	require.Equal(t, apperrors.CodeTooManyRequests, apperrors.CodeOf(err))

	h.clock.Advance(h.cfg.OTPResendCooldown)
	_, err = h.svc.RequestOTP(ctx, testPhone, "")
	require.NoError(t, err)
	require.Equal(t, 2, h.sms.count())
}

func TestRequestOTPEnforcesDailyCap(t *testing.T) {
	h := newHarness(t)
	for range h.cfg.OTPMaxPerPhoneDay {
		_, err := h.svc.RequestOTP(ctx, testPhone, "")
		require.NoError(t, err)
		h.clock.Advance(h.cfg.OTPResendCooldown)
	}

	_, err := h.svc.RequestOTP(ctx, testPhone, "")
	require.Equal(t, apperrors.CodeTooManyRequests, apperrors.CodeOf(err))
	require.Contains(t, apperrors.SafeMessage(err), "daily")

	h.clock.Advance(otpDailyWindow)
	_, err = h.svc.RequestOTP(ctx, testPhone, "")
	require.NoError(t, err)
}

func TestRequestOTPRejectsSuspendedUser(t *testing.T) {
	h := newHarness(t)
	h.login(t, testPhone)
	h.users.setStatus(testPhone, users.StatusSuspended)

	_, err := h.svc.RequestOTP(ctx, testPhone, "")
	require.Equal(t, apperrors.CodeAccountSuspended, apperrors.CodeOf(err))
}

func TestRequestOTPSMSFailureIsUnavailable(t *testing.T) {
	h := newHarness(t)
	h.sms.err = errors.New("provider down")

	_, err := h.svc.RequestOTP(ctx, testPhone, "")
	require.Equal(t, apperrors.CodeUnavailable, apperrors.CodeOf(err))
	require.NotContains(t, apperrors.SafeMessage(err), "provider down")
}

func TestTestPhoneUsesFixedCodeWithoutSMS(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.RequestOTP(ctx, "+919999999999", "")
	require.NoError(t, err)
	require.Zero(t, h.sms.count())

	res, err := h.svc.VerifyOTP(ctx, "+919999999999", "123456", ClientInfo{})
	require.NoError(t, err)
	require.True(t, res.IsNewUser)
}

// --- VerifyOTP ---

func TestVerifyOTPCreatesUserThenSignsInExisting(t *testing.T) {
	h := newHarness(t)

	first := h.login(t, testPhone)
	require.True(t, first.IsNewUser)
	require.Equal(t, testPhone, first.User.PhoneE164)
	require.Equal(t, users.StatusActive, first.User.Status)
	require.NotEmpty(t, first.Tokens.AccessToken)
	require.NotEmpty(t, first.Tokens.RefreshToken)

	second := h.login(t, testPhone)
	require.False(t, second.IsNewUser)
	require.Equal(t, first.User.ID, second.User.ID)
	require.NotEqual(t, first.Tokens.SessionID, second.Tokens.SessionID, "each login is a separate session")

	// Multi-device: the first session is still valid.
	_, err := h.svc.Authenticate(ctx, first.Tokens.AccessToken)
	require.NoError(t, err)
}

func TestVerifyOTPRecordsSessionMetadata(t *testing.T) {
	h := newHarness(t)
	res := h.login(t, testPhone)

	s := h.store.session(res.Tokens.SessionID)
	require.Equal(t, "Pixel 9", *s.DeviceName)
	require.Equal(t, "203.0.113.7", *s.IPAddress)
}

func TestVerifyOTPInvalidCodePersistsAttempts(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.RequestOTP(ctx, testPhone, "")
	require.NoError(t, err)
	code := h.sms.lastCode(t)

	_, err = h.svc.VerifyOTP(ctx, testPhone, wrongCode(code), ClientInfo{})
	require.Equal(t, apperrors.CodeOTPInvalid, apperrors.CodeOf(err))
	require.Equal(t, 1, h.store.latest().Attempts, "failed attempt must be recorded")

	_, err = h.svc.VerifyOTP(ctx, testPhone, wrongCode(code), ClientInfo{})
	require.Equal(t, apperrors.CodeOTPInvalid, apperrors.CodeOf(err))

	_, err = h.svc.VerifyOTP(ctx, testPhone, wrongCode(code), ClientInfo{})
	require.Equal(t, apperrors.CodeOTPAttemptsExceeded, apperrors.CodeOf(err))

	// Even the correct code is rejected once the challenge is exhausted.
	_, err = h.svc.VerifyOTP(ctx, testPhone, code, ClientInfo{})
	require.Equal(t, apperrors.CodeOTPAttemptsExceeded, apperrors.CodeOf(err))
}

func TestVerifyOTPExpired(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.RequestOTP(ctx, testPhone, "")
	require.NoError(t, err)

	h.clock.Advance(h.cfg.OTPTTL)
	_, err = h.svc.VerifyOTP(ctx, testPhone, h.sms.lastCode(t), ClientInfo{})
	require.Equal(t, apperrors.CodeOTPExpired, apperrors.CodeOf(err))
}

func TestVerifyOTPCannotBeReused(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.RequestOTP(ctx, testPhone, "")
	require.NoError(t, err)
	code := h.sms.lastCode(t)

	_, err = h.svc.VerifyOTP(ctx, testPhone, code, ClientInfo{})
	require.NoError(t, err)

	_, err = h.svc.VerifyOTP(ctx, testPhone, code, ClientInfo{})
	require.Equal(t, apperrors.CodeOTPInvalid, apperrors.CodeOf(err))
}

func TestVerifyOTPOnlyLatestChallengeCounts(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.RequestOTP(ctx, testPhone, "")
	require.NoError(t, err)
	oldCode := h.sms.lastCode(t)

	h.clock.Advance(h.cfg.OTPResendCooldown)
	_, err = h.svc.RequestOTP(ctx, testPhone, "")
	require.NoError(t, err)
	newCode := h.sms.lastCode(t)

	if oldCode != newCode {
		_, err = h.svc.VerifyOTP(ctx, testPhone, oldCode, ClientInfo{})
		require.Equal(t, apperrors.CodeOTPInvalid, apperrors.CodeOf(err))
	}
	_, err = h.svc.VerifyOTP(ctx, testPhone, newCode, ClientInfo{})
	require.NoError(t, err)
}

func TestVerifyOTPWithoutChallenge(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.VerifyOTP(ctx, testPhone, "123456", ClientInfo{})
	require.Equal(t, apperrors.CodeOTPInvalid, apperrors.CodeOf(err))
}

func TestVerifyOTPValidatesInput(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.VerifyOTP(ctx, "123", "12ab", ClientInfo{})
	require.Equal(t, apperrors.CodeInvalid, apperrors.CodeOf(err))
	require.Contains(t, apperrors.Details(err), "phone")
	require.Contains(t, apperrors.Details(err), "code")
}

func TestVerifyOTPSuspendedUserConsumesButRejects(t *testing.T) {
	h := newHarness(t)
	h.login(t, testPhone)
	_, err := h.svc.RequestOTP(ctx, testPhone, "")
	require.NoError(t, err)
	h.users.setStatus(testPhone, users.StatusSuspended)

	_, err = h.svc.VerifyOTP(ctx, testPhone, h.sms.lastCode(t), ClientInfo{})
	require.Equal(t, apperrors.CodeAccountSuspended, apperrors.CodeOf(err))
	require.NotNil(t, h.store.latest().ConsumedAt)
}

// --- Refresh ---

func TestRefreshRotatesToken(t *testing.T) {
	h := newHarness(t)
	login := h.login(t, testPhone)

	h.clock.Advance(time.Minute)
	pair, err := h.svc.Refresh(ctx, login.Tokens.RefreshToken)
	require.NoError(t, err)
	require.NotEqual(t, login.Tokens.RefreshToken, pair.RefreshToken)
	require.Equal(t, login.Tokens.SessionID, pair.SessionID)
	require.True(t, pair.RefreshExpiresAt.After(login.Tokens.RefreshExpiresAt), "session slides forward")

	_, err = h.svc.Authenticate(ctx, pair.AccessToken)
	require.NoError(t, err)

	// The rotated token can itself be refreshed.
	_, err = h.svc.Refresh(ctx, pair.RefreshToken)
	require.NoError(t, err)
}

func TestRefreshReuseRevokesSession(t *testing.T) {
	h := newHarness(t)
	login := h.login(t, testPhone)

	rotated, err := h.svc.Refresh(ctx, login.Tokens.RefreshToken)
	require.NoError(t, err)

	_, err = h.svc.Refresh(ctx, login.Tokens.RefreshToken)
	require.Equal(t, apperrors.CodeRefreshTokenInvalid, apperrors.CodeOf(err))
	require.Equal(t, RevokeRefreshReuse, h.store.session(login.Tokens.SessionID).reason)

	// The legitimate successor is now dead too, as is its access token.
	_, err = h.svc.Refresh(ctx, rotated.RefreshToken)
	require.Equal(t, apperrors.CodeRefreshTokenInvalid, apperrors.CodeOf(err))
	_, err = h.svc.Authenticate(ctx, rotated.AccessToken)
	require.Equal(t, apperrors.CodeSessionRevoked, apperrors.CodeOf(err))
}

func TestRefreshUnknownAndEmptyToken(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.Refresh(ctx, "not-a-real-token")
	require.Equal(t, apperrors.CodeRefreshTokenInvalid, apperrors.CodeOf(err))
	_, err = h.svc.Refresh(ctx, "")
	require.Equal(t, apperrors.CodeRefreshTokenInvalid, apperrors.CodeOf(err))
}

func TestRefreshExpired(t *testing.T) {
	h := newHarness(t)
	login := h.login(t, testPhone)

	h.clock.Advance(h.cfg.RefreshTokenTTL)
	_, err := h.svc.Refresh(ctx, login.Tokens.RefreshToken)
	require.Equal(t, apperrors.CodeRefreshTokenInvalid, apperrors.CodeOf(err))
	require.Contains(t, apperrors.SafeMessage(err), "expired")
}

func TestRefreshSuspendedUserRevokesSession(t *testing.T) {
	h := newHarness(t)
	login := h.login(t, testPhone)
	h.users.setStatus(testPhone, users.StatusSuspended)

	_, err := h.svc.Refresh(ctx, login.Tokens.RefreshToken)
	require.Equal(t, apperrors.CodeAccountSuspended, apperrors.CodeOf(err))
	require.Equal(t, RevokeSuspended, h.store.session(login.Tokens.SessionID).reason)
}

// --- Logout ---

func TestLogoutRevokesSessionAndRefresh(t *testing.T) {
	h := newHarness(t)
	login := h.login(t, testPhone)
	p, err := h.svc.Authenticate(ctx, login.Tokens.AccessToken)
	require.NoError(t, err)

	require.NoError(t, h.svc.Logout(ctx, p))
	require.NoError(t, h.svc.Logout(ctx, p), "logout is idempotent")

	_, err = h.svc.Authenticate(ctx, login.Tokens.AccessToken)
	require.Equal(t, apperrors.CodeSessionRevoked, apperrors.CodeOf(err))
	_, err = h.svc.Refresh(ctx, login.Tokens.RefreshToken)
	require.Equal(t, apperrors.CodeRefreshTokenInvalid, apperrors.CodeOf(err))
}

func TestLogoutAllRevokesEverySession(t *testing.T) {
	h := newHarness(t)
	a := h.login(t, testPhone)
	b := h.login(t, testPhone)
	other := h.login(t, "+919000000001")

	p, err := h.svc.Authenticate(ctx, a.Tokens.AccessToken)
	require.NoError(t, err)
	n, err := h.svc.LogoutAll(ctx, p)
	require.NoError(t, err)
	require.Equal(t, 2, n)

	for _, tok := range []string{a.Tokens.AccessToken, b.Tokens.AccessToken} {
		_, err = h.svc.Authenticate(ctx, tok)
		require.Equal(t, apperrors.CodeSessionRevoked, apperrors.CodeOf(err))
	}
	_, err = h.svc.Authenticate(ctx, other.Tokens.AccessToken)
	require.NoError(t, err, "other users are unaffected")
}

// --- Authenticate ---

func TestAuthenticateExpiredAccessToken(t *testing.T) {
	h := newHarness(t)
	login := h.login(t, testPhone)

	h.clock.Advance(16 * time.Minute)
	_, err := h.svc.Authenticate(ctx, login.Tokens.AccessToken)
	require.Equal(t, apperrors.CodeAccessTokenExpired, apperrors.CodeOf(err))
}

func TestAuthenticateSuspendedUser(t *testing.T) {
	h := newHarness(t)
	login := h.login(t, testPhone)
	h.users.setStatus(testPhone, users.StatusSuspended)

	_, err := h.svc.Authenticate(ctx, login.Tokens.AccessToken)
	require.Equal(t, apperrors.CodeAccountSuspended, apperrors.CodeOf(err))
}

func TestAuthenticateGarbageToken(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.Authenticate(ctx, "garbage")
	require.Equal(t, apperrors.CodeAccessTokenInvalid, apperrors.CodeOf(err))
}

func TestAuthenticateTamperedToken(t *testing.T) {
	h := newHarness(t)
	login := h.login(t, testPhone)
	parts := strings.Split(login.Tokens.AccessToken, ".")
	parts[2] = strings.Repeat("A", len(parts[2]))

	_, err := h.svc.Authenticate(ctx, strings.Join(parts, "."))
	require.Equal(t, apperrors.CodeAccessTokenInvalid, apperrors.CodeOf(err))
}
