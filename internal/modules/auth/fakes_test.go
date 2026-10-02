package auth

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/adi6859/travel-swipe-backend/internal/modules/users"
	"github.com/adi6859/travel-swipe-backend/pkg/clock"
)

const (
	testJWTSecret = "jwt-secret-0123456789abcdef0123456789"
	testOTPSecret = "otp-secret-0123456789abcdef0123456789"
	testPhone     = "+919876543210"
)

type fakeTx struct{}

func (fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type fakeUsers struct {
	mu      sync.Mutex
	byPhone map[string]users.User
	deleted map[uuid.UUID]bool
}

func newFakeUsers() *fakeUsers {
	return &fakeUsers{byPhone: map[string]users.User{}, deleted: map[uuid.UUID]bool{}}
}

func (f *fakeUsers) FindByPhone(_ context.Context, phone string) (users.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.byPhone[phone]
	if !ok || f.deleted[u.ID] {
		return users.User{}, users.ErrNotFound
	}
	return u, nil
}

func (f *fakeUsers) Create(_ context.Context, u users.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing, ok := f.byPhone[u.PhoneE164]; ok && !f.deleted[existing.ID] {
		return users.ErrPhoneTaken
	}
	f.byPhone[u.PhoneE164] = u
	return nil
}

func (f *fakeUsers) RecordLogin(_ context.Context, id uuid.UUID, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for p, u := range f.byPhone {
		if u.ID == id {
			u.LastLoginAt = &at
			f.byPhone[p] = u
		}
	}
	return nil
}

func (f *fakeUsers) setStatus(phone string, status users.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.byPhone[phone]
	u.Status = status
	f.byPhone[phone] = u
}

func (f *fakeUsers) byID(id uuid.UUID) (users.User, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.byPhone {
		if u.ID == id {
			return u, true
		}
	}
	return users.User{}, false
}

type fakeSession struct {
	Session
	revokedAt *time.Time
	reason    RevokeReason
}

type fakeStore struct {
	mu         sync.Mutex
	users      *fakeUsers
	challenges []OTPChallenge
	sessions   map[uuid.UUID]*fakeSession
	tokens     map[string]*RefreshToken
}

func newFakeStore(u *fakeUsers) *fakeStore {
	return &fakeStore{users: u, sessions: map[uuid.UUID]*fakeSession{}, tokens: map[string]*RefreshToken{}}
}

var _ Store = (*fakeStore)(nil)

func (f *fakeStore) LockPhone(context.Context, string) error { return nil }

func (f *fakeStore) LatestChallenge(_ context.Context, phone, purpose string) (OTPChallenge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.challenges) - 1; i >= 0; i-- {
		if c := f.challenges[i]; c.PhoneE164 == phone && c.Purpose == purpose {
			return c, nil
		}
	}
	return OTPChallenge{}, ErrNotFound
}

func (f *fakeStore) CountChallengesSince(_ context.Context, phone, purpose string, since time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.challenges {
		if c.PhoneE164 == phone && c.Purpose == purpose && !c.CreatedAt.Before(since) {
			n++
		}
	}
	return n, nil
}

func (f *fakeStore) CreateChallenge(_ context.Context, c OTPChallenge) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.challenges = append(f.challenges, c)
	return nil
}

func (f *fakeStore) IncrementChallengeAttempts(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.challenges {
		if f.challenges[i].ID == id && f.challenges[i].Attempts < f.challenges[i].MaxAttempts {
			f.challenges[i].Attempts++
		}
	}
	return nil
}

func (f *fakeStore) ConsumeChallenge(_ context.Context, id uuid.UUID, at time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.challenges {
		if f.challenges[i].ID == id && f.challenges[i].ConsumedAt == nil {
			f.challenges[i].ConsumedAt = &at
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) CreateSession(_ context.Context, s Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[s.ID] = &fakeSession{Session: s}
	return nil
}

func (f *fakeStore) SessionState(_ context.Context, id uuid.UUID, _ bool) (SessionState, error) {
	f.mu.Lock()
	s, ok := f.sessions[id]
	f.mu.Unlock()
	if !ok {
		return SessionState{}, ErrNotFound
	}
	u, _ := f.users.byID(s.UserID)
	st := SessionState{SessionID: s.ID, UserID: s.UserID, ExpiresAt: s.ExpiresAt, RevokedAt: s.revokedAt, UserStatus: u.Status}
	if f.users.deleted[u.ID] {
		now := time.Now()
		st.UserDeletedAt = &now
	}
	return st, nil
}

func (f *fakeStore) ExtendSession(_ context.Context, id uuid.UUID, lastSeen, expiresAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sessions[id]
	s.LastSeenAt, s.ExpiresAt = lastSeen, expiresAt
	return nil
}

func (f *fakeStore) RevokeSession(_ context.Context, id uuid.UUID, reason RevokeReason, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.sessions[id]; ok && s.revokedAt == nil {
		s.revokedAt, s.reason = &at, reason
	}
	return nil
}

func (f *fakeStore) RevokeUserSessions(_ context.Context, userID uuid.UUID, reason RevokeReason, at time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, s := range f.sessions {
		if s.UserID == userID && s.revokedAt == nil {
			s.revokedAt, s.reason = &at, reason
			n++
		}
	}
	return n, nil
}

func (f *fakeStore) CreateRefreshToken(_ context.Context, t RefreshToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.tokens {
		if existing.SessionID == t.SessionID && existing.UsedAt == nil {
			panic("fake store: second unused refresh token for session (violates uq_refresh_tokens_session_unused)")
		}
	}
	f.tokens[t.TokenHash] = &t
	return nil
}

func (f *fakeStore) RefreshTokenForUpdate(_ context.Context, hash string) (RefreshToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tokens[hash]
	if !ok {
		return RefreshToken{}, ErrNotFound
	}
	return *t, nil
}

func (f *fakeStore) MarkRefreshTokenUsed(_ context.Context, id uuid.UUID, at time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tokens {
		if t.ID == id && t.UsedAt == nil {
			t.UsedAt = &at
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) session(id uuid.UUID) fakeSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.sessions[id]
}

func (f *fakeStore) latest() OTPChallenge {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.challenges[len(f.challenges)-1]
}

type sentSMS struct{ phone, code string }

type fakeSMS struct {
	mu   sync.Mutex
	sent []sentSMS
	err  error
}

func (f *fakeSMS) SendOTP(_ context.Context, phone, code string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, sentSMS{phone, code})
	return nil
}

func (f *fakeSMS) lastCode(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		t.Fatal("no sms sent")
	}
	return f.sent[len(f.sent)-1].code
}

func (f *fakeSMS) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

type harness struct {
	svc   *Service
	store *fakeStore
	users *fakeUsers
	sms   *fakeSMS
	clock *clock.MockClock
	cfg   Config
}

func testConfig() Config {
	return Config{
		OTPLength:         6,
		OTPTTL:            5 * time.Minute,
		OTPResendCooldown: 30 * time.Second,
		OTPMaxAttempts:    3,
		OTPMaxPerPhoneDay: 4,
		RefreshTokenTTL:   30 * 24 * time.Hour,
		TestPhoneOTPs:     map[string]string{"+919999999999": "123456"},
	}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	cfg := testConfig()
	clk := &clock.MockClock{Current: time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)}
	u := newFakeUsers()
	store := newFakeStore(u)
	sms := &fakeSMS{}
	svc := NewService(cfg, store, u, fakeTx{},
		NewTokenIssuer(testJWTSecret, "test-iss", "test-aud", 15*time.Minute, clk),
		NewSecrets(testOTPSecret), sms, clk, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	return &harness{svc: svc, store: store, users: u, sms: sms, clock: clk, cfg: cfg}
}

// login runs request + verify for phone and returns the result.
func (h *harness) login(t *testing.T, phone string) LoginResult {
	t.Helper()
	ctx := context.Background()
	if _, err := h.svc.RequestOTP(ctx, phone, "203.0.113.7"); err != nil {
		t.Fatalf("request otp: %v", err)
	}
	res, err := h.svc.VerifyOTP(ctx, phone, h.sms.lastCode(t), ClientInfo{DeviceName: "Pixel 9", UserAgent: "ua", IP: "203.0.113.7"})
	if err != nil {
		t.Fatalf("verify otp: %v", err)
	}
	h.clock.Advance(h.cfg.OTPResendCooldown)
	return res
}
