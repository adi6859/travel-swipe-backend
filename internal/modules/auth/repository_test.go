package auth

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/adi6859/travel-swipe-backend/internal/modules/users"
	"github.com/adi6859/travel-swipe-backend/internal/platform/database"
	"github.com/adi6859/travel-swipe-backend/internal/platform/database/dbtest"
	"github.com/adi6859/travel-swipe-backend/pkg/clock"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
)

type pgHarness struct {
	svc   *Service
	store *PostgresStore
	sms   *fakeSMS
	clock *clock.MockClock
	cfg   Config
}

// newPGHarness wires the real Postgres store, users repository, and
// transaction manager. Postgres timestamps are microsecond precision, so the
// clock is truncated accordingly.
func newPGHarness(t *testing.T) *pgHarness {
	t.Helper()
	db := dbtest.Open(t)
	cfg := testConfig()
	clk := &clock.MockClock{Current: time.Now().UTC().Truncate(time.Second)}
	store := NewPostgresStore(db)
	sms := &fakeSMS{}
	svc := NewService(cfg, store, users.NewRepository(db), database.NewTxManager(db),
		NewTokenIssuer(testJWTSecret, "test-iss", "test-aud", 15*time.Minute, clk),
		NewSecrets(testOTPSecret), sms, clk, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	return &pgHarness{svc: svc, store: store, sms: sms, clock: clk, cfg: cfg}
}

func (h *pgHarness) login(t *testing.T, phone string) LoginResult {
	t.Helper()
	_, err := h.svc.RequestOTP(ctx, phone, "203.0.113.7")
	require.NoError(t, err)
	res, err := h.svc.VerifyOTP(ctx, phone, h.sms.lastCode(t), ClientInfo{DeviceName: "Pixel 9", UserAgent: "ua", IP: "203.0.113.7"})
	require.NoError(t, err)
	h.clock.Advance(h.cfg.OTPResendCooldown)
	return res
}

func TestPGLoginRefreshLogout(t *testing.T) {
	h := newPGHarness(t)

	first := h.login(t, testPhone)
	require.True(t, first.IsNewUser)
	second := h.login(t, testPhone)
	require.False(t, second.IsNewUser)
	require.Equal(t, first.User.ID, second.User.ID)

	p, err := h.svc.Authenticate(ctx, first.Tokens.AccessToken)
	require.NoError(t, err)
	require.Equal(t, first.User.ID, p.UserID)

	rotated, err := h.svc.Refresh(ctx, first.Tokens.RefreshToken)
	require.NoError(t, err)
	require.NotEqual(t, first.Tokens.RefreshToken, rotated.RefreshToken)

	require.NoError(t, h.svc.Logout(ctx, p))
	_, err = h.svc.Authenticate(ctx, rotated.AccessToken)
	require.Equal(t, apperrors.CodeSessionRevoked, apperrors.CodeOf(err))
	_, err = h.svc.Refresh(ctx, rotated.RefreshToken)
	require.Equal(t, apperrors.CodeRefreshTokenInvalid, apperrors.CodeOf(err))

	_, err = h.svc.Authenticate(ctx, second.Tokens.AccessToken)
	require.NoError(t, err, "other device session survives single logout")
}

func TestPGFailedAttemptsAreCommitted(t *testing.T) {
	h := newPGHarness(t)
	_, err := h.svc.RequestOTP(ctx, testPhone, "")
	require.NoError(t, err)
	code := h.sms.lastCode(t)

	for i := 1; i < h.cfg.OTPMaxAttempts; i++ {
		_, err = h.svc.VerifyOTP(ctx, testPhone, wrongCode(code), ClientInfo{})
		require.Equal(t, apperrors.CodeOTPInvalid, apperrors.CodeOf(err))
		c, err := h.store.LatestChallenge(ctx, testPhone, PurposeLogin)
		require.NoError(t, err)
		require.Equal(t, i, c.Attempts, "attempt %d must survive the transaction", i)
	}
	_, err = h.svc.VerifyOTP(ctx, testPhone, wrongCode(code), ClientInfo{})
	require.Equal(t, apperrors.CodeOTPAttemptsExceeded, apperrors.CodeOf(err))
	_, err = h.svc.VerifyOTP(ctx, testPhone, code, ClientInfo{})
	require.Equal(t, apperrors.CodeOTPAttemptsExceeded, apperrors.CodeOf(err))
}

func TestPGConcurrentWrongGuessesNeverExceedMax(t *testing.T) {
	h := newPGHarness(t)
	_, err := h.svc.RequestOTP(ctx, testPhone, "")
	require.NoError(t, err)
	bad := wrongCode(h.sms.lastCode(t))

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = h.svc.VerifyOTP(ctx, testPhone, bad, ClientInfo{})
		}()
	}
	wg.Wait()

	c, err := h.store.LatestChallenge(ctx, testPhone, PurposeLogin)
	require.NoError(t, err)
	require.Equal(t, h.cfg.OTPMaxAttempts, c.Attempts)
}

func TestPGConcurrentRefreshOnlyOneWins(t *testing.T) {
	h := newPGHarness(t)
	login := h.login(t, testPhone)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		success int
	)
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := h.svc.Refresh(ctx, login.Tokens.RefreshToken); err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	require.Equal(t, 1, success)

	// Losers presented a used token, which revokes the session.
	st, err := h.store.SessionState(ctx, login.Tokens.SessionID, false)
	require.NoError(t, err)
	require.NotNil(t, st.RevokedAt)
}

func TestPGConcurrentFirstLoginCreatesOneUser(t *testing.T) {
	h := newPGHarness(t)
	_, err := h.svc.RequestOTP(ctx, "+919999999999", "")
	require.NoError(t, err)

	var wg sync.WaitGroup
	results := make(chan LoginResult, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if res, err := h.svc.VerifyOTP(ctx, "+919999999999", "123456", ClientInfo{}); err == nil {
				results <- res
			}
		}()
	}
	wg.Wait()
	close(results)

	n := 0
	for range results {
		n++
	}
	require.Equal(t, 1, n, "an OTP can be consumed exactly once")
}

func TestPGLogoutAll(t *testing.T) {
	h := newPGHarness(t)
	a := h.login(t, testPhone)
	b := h.login(t, testPhone)

	p, err := h.svc.Authenticate(ctx, a.Tokens.AccessToken)
	require.NoError(t, err)
	n, err := h.svc.LogoutAll(ctx, p)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	_, err = h.svc.Authenticate(ctx, b.Tokens.AccessToken)
	require.Equal(t, apperrors.CodeSessionRevoked, apperrors.CodeOf(err))
}

func TestPGDailyCapCountsPersistedChallenges(t *testing.T) {
	h := newPGHarness(t)
	for range h.cfg.OTPMaxPerPhoneDay {
		_, err := h.svc.RequestOTP(ctx, testPhone, "")
		require.NoError(t, err)
		h.clock.Advance(h.cfg.OTPResendCooldown)
	}
	_, err := h.svc.RequestOTP(ctx, testPhone, "")
	require.Equal(t, apperrors.CodeTooManyRequests, apperrors.CodeOf(err))
}
