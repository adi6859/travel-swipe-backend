package users

import (
	"context"
	"database/sql"
	stdErrors "errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"

	"github.com/adi6859/travel-swipe-backend/internal/platform/database"
)

const pgUniqueViolation = "23505"

const userColumns = `id, phone_e164, status, phone_verified_at, last_login_at, created_at, updated_at`

// Repository is the PostgreSQL store for users and profiles. Methods join the
// caller's transaction when one is present in ctx.
type Repository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) FindByPhone(ctx context.Context, phoneE164 string) (User, error) {
	var u User
	err := database.Conn(ctx, r.db).GetContext(ctx, &u,
		`SELECT `+userColumns+` FROM users WHERE phone_e164 = $1 AND deleted_at IS NULL`, phoneE164)
	return u, mapNotFound(err)
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (User, error) {
	var u User
	err := database.Conn(ctx, r.db).GetContext(ctx, &u,
		`SELECT `+userColumns+` FROM users WHERE id = $1 AND deleted_at IS NULL`, id)
	return u, mapNotFound(err)
}

// Create inserts the user and an empty profile. Callers should run it inside a
// transaction so both rows commit together.
func (r *Repository) Create(ctx context.Context, u User) error {
	conn := database.Conn(ctx, r.db)
	_, err := conn.ExecContext(ctx,
		`INSERT INTO users (id, phone_e164, status, phone_verified_at, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		u.ID, u.PhoneE164, u.Status, u.PhoneVerifiedAt, u.CreatedAt, u.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if stdErrors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return ErrPhoneTaken
		}
		return err
	}
	_, err = conn.ExecContext(ctx,
		`INSERT INTO user_profiles (user_id, created_at, updated_at) VALUES ($1, $2, $2)`,
		u.ID, u.CreatedAt)
	return err
}

func (r *Repository) RecordLogin(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := database.Conn(ctx, r.db).ExecContext(ctx,
		`UPDATE users SET last_login_at = $2, updated_at = $2 WHERE id = $1`, id, at)
	return err
}

func mapNotFound(err error) error {
	if stdErrors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
