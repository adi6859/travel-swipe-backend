package auth

import (
	"context"
	"database/sql"
	stdErrors "errors"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/adi6859/travel-swipe-backend/internal/platform/database"
)

// PostgresStore implements Store.
type PostgresStore struct {
	db *sqlx.DB
}

func NewPostgresStore(db *sqlx.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

var _ Store = (*PostgresStore)(nil)

func (s *PostgresStore) conn(ctx context.Context) database.Executor {
	return database.Conn(ctx, s.db)
}

func (s *PostgresStore) LockPhone(ctx context.Context, phoneE164 string) error {
	_, err := s.conn(ctx).ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended('otp:' || $1, 0))`, phoneE164)
	return err
}

const challengeColumns = `id, phone_e164, purpose, code_hash, attempts, max_attempts,
	expires_at, resend_after, consumed_at, created_at`

func (s *PostgresStore) LatestChallenge(ctx context.Context, phoneE164, purpose string) (OTPChallenge, error) {
	var c OTPChallenge
	err := s.conn(ctx).GetContext(ctx, &c,
		`SELECT `+challengeColumns+` FROM otp_challenges
		 WHERE phone_e164 = $1 AND purpose = $2
		 ORDER BY created_at DESC, id DESC LIMIT 1`, phoneE164, purpose)
	return c, mapNotFound(err)
}

func (s *PostgresStore) CountChallengesSince(ctx context.Context, phoneE164, purpose string, since time.Time) (int, error) {
	var n int
	err := s.conn(ctx).GetContext(ctx, &n,
		`SELECT count(*) FROM otp_challenges WHERE phone_e164 = $1 AND purpose = $2 AND created_at >= $3`,
		phoneE164, purpose, since)
	return n, err
}

func (s *PostgresStore) CreateChallenge(ctx context.Context, c OTPChallenge) error {
	_, err := s.conn(ctx).ExecContext(ctx,
		`INSERT INTO otp_challenges
		   (id, phone_e164, purpose, code_hash, attempts, max_attempts, expires_at, resend_after, request_ip, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::inet, $10)`,
		c.ID, c.PhoneE164, c.Purpose, c.CodeHash, c.Attempts, c.MaxAttempts,
		c.ExpiresAt, c.ResendAfter, c.RequestIP, c.CreatedAt)
	return err
}

func (s *PostgresStore) IncrementChallengeAttempts(ctx context.Context, id uuid.UUID) error {
	_, err := s.conn(ctx).ExecContext(ctx,
		`UPDATE otp_challenges SET attempts = attempts + 1 WHERE id = $1 AND attempts < max_attempts`, id)
	return err
}

func (s *PostgresStore) ConsumeChallenge(ctx context.Context, id uuid.UUID, at time.Time) (bool, error) {
	res, err := s.conn(ctx).ExecContext(ctx,
		`UPDATE otp_challenges SET consumed_at = $2 WHERE id = $1 AND consumed_at IS NULL`, id, at)
	return affectedOne(res, err)
}

func (s *PostgresStore) CreateSession(ctx context.Context, sess Session) error {
	_, err := s.conn(ctx).ExecContext(ctx,
		`INSERT INTO auth_sessions (id, user_id, device_name, user_agent, ip_address, created_at, last_seen_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5::inet, $6, $7, $8)`,
		sess.ID, sess.UserID, sess.DeviceName, sess.UserAgent, sess.IPAddress,
		sess.CreatedAt, sess.LastSeenAt, sess.ExpiresAt)
	return err
}

func (s *PostgresStore) SessionState(ctx context.Context, id uuid.UUID, forUpdate bool) (SessionState, error) {
	query := `SELECT s.id, s.user_id, s.expires_at, s.revoked_at,
	                 u.status AS user_status, u.deleted_at AS user_deleted_at
	          FROM auth_sessions s JOIN users u ON u.id = s.user_id
	          WHERE s.id = $1`
	if forUpdate {
		query += ` FOR UPDATE OF s`
	}
	var st SessionState
	err := s.conn(ctx).GetContext(ctx, &st, query, id)
	return st, mapNotFound(err)
}

func (s *PostgresStore) ExtendSession(ctx context.Context, id uuid.UUID, lastSeen, expiresAt time.Time) error {
	_, err := s.conn(ctx).ExecContext(ctx,
		`UPDATE auth_sessions SET last_seen_at = $2, expires_at = $3 WHERE id = $1`, id, lastSeen, expiresAt)
	return err
}

func (s *PostgresStore) RevokeSession(ctx context.Context, id uuid.UUID, reason RevokeReason, at time.Time) error {
	_, err := s.conn(ctx).ExecContext(ctx,
		`UPDATE auth_sessions SET revoked_at = $2, revoke_reason = $3 WHERE id = $1 AND revoked_at IS NULL`,
		id, at, string(reason))
	return err
}

func (s *PostgresStore) RevokeUserSessions(ctx context.Context, userID uuid.UUID, reason RevokeReason, at time.Time) (int, error) {
	res, err := s.conn(ctx).ExecContext(ctx,
		`UPDATE auth_sessions SET revoked_at = $2, revoke_reason = $3 WHERE user_id = $1 AND revoked_at IS NULL`,
		userID, at, string(reason))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func (s *PostgresStore) CreateRefreshToken(ctx context.Context, t RefreshToken) error {
	_, err := s.conn(ctx).ExecContext(ctx,
		`INSERT INTO refresh_tokens (id, session_id, token_hash, expires_at, created_at) VALUES ($1, $2, $3, $4, $5)`,
		t.ID, t.SessionID, t.TokenHash, t.ExpiresAt, t.CreatedAt)
	return err
}

func (s *PostgresStore) RefreshTokenForUpdate(ctx context.Context, tokenHash string) (RefreshToken, error) {
	var t RefreshToken
	err := s.conn(ctx).GetContext(ctx, &t,
		`SELECT id, session_id, token_hash, expires_at, used_at, created_at
		 FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE`, tokenHash)
	return t, mapNotFound(err)
}

func (s *PostgresStore) MarkRefreshTokenUsed(ctx context.Context, id uuid.UUID, at time.Time) (bool, error) {
	res, err := s.conn(ctx).ExecContext(ctx,
		`UPDATE refresh_tokens SET used_at = $2 WHERE id = $1 AND used_at IS NULL`, id, at)
	return affectedOne(res, err)
}

func affectedOne(res sql.Result, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func mapNotFound(err error) error {
	if stdErrors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
