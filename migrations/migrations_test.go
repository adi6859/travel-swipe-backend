package migrations_test

import (
	"context"
	"io/fs"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/adi6859/travel-swipe-backend/internal/platform/database/dbtest"
	"github.com/adi6859/travel-swipe-backend/migrations"
)

func TestEmbeddedMigrationsPresent(t *testing.T) {
	t.Parallel()

	names, err := fs.Glob(migrations.Files(), "*.sql")
	require.NoError(t, err)
	require.Contains(t, names, "00001_auth_schema.sql")
}

func TestMigrationsDownUp(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()

	require.NoError(t, migrations.RunWithDB(ctx, db.DB, "down"))
	require.NoError(t, migrations.RunWithDB(ctx, db.DB, "up"))
}

func TestSchemaConstraints(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()

	now := time.Now().UTC()
	insertUser := func(id uuid.UUID, phone string) error {
		_, err := db.ExecContext(ctx,
			`INSERT INTO users (id, phone_e164, phone_verified_at) VALUES ($1, $2, $3)`, id, phone, now)
		return err
	}

	userID := uuid.Must(uuid.NewV7())
	require.NoError(t, insertUser(userID, "+919876543210"))

	t.Run("duplicate active phone rejected", func(t *testing.T) {
		require.Error(t, insertUser(uuid.Must(uuid.NewV7()), "+919876543210"))
	})

	t.Run("soft-deleted phone can be reused", func(t *testing.T) {
		_, err := db.ExecContext(ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, userID)
		require.NoError(t, err)
		require.NoError(t, insertUser(uuid.Must(uuid.NewV7()), "+919876543210"))
	})

	t.Run("non E.164 phone rejected", func(t *testing.T) {
		require.Error(t, insertUser(uuid.Must(uuid.NewV7()), "9876543210"))
	})

	t.Run("one unused refresh token per session", func(t *testing.T) {
		owner := uuid.Must(uuid.NewV7())
		require.NoError(t, insertUser(owner, "+919000000001"))
		sessionID := uuid.Must(uuid.NewV7())
		_, err := db.ExecContext(ctx,
			`INSERT INTO auth_sessions (id, user_id, expires_at) VALUES ($1, $2, $3)`,
			sessionID, owner, now.Add(time.Hour))
		require.NoError(t, err)

		insertToken := func(hash string) error {
			_, err := db.ExecContext(ctx,
				`INSERT INTO refresh_tokens (id, session_id, token_hash, expires_at) VALUES ($1, $2, $3, $4)`,
				uuid.Must(uuid.NewV7()), sessionID, hash, now.Add(time.Hour))
			return err
		}
		require.NoError(t, insertToken("hash-a"))
		require.Error(t, insertToken("hash-b"), "second unused token must violate partial unique index")
		require.Error(t, insertToken("hash-a"), "duplicate hash must violate unique constraint")
	})

	t.Run("revoked_at and revoke_reason must be set together", func(t *testing.T) {
		owner := uuid.Must(uuid.NewV7())
		require.NoError(t, insertUser(owner, "+919000000002"))
		_, err := db.ExecContext(ctx,
			`INSERT INTO auth_sessions (id, user_id, expires_at, revoked_at) VALUES ($1, $2, $3, now())`,
			uuid.Must(uuid.NewV7()), owner, now.Add(time.Hour))
		require.Error(t, err)
	})

	t.Run("otp attempts cannot exceed max", func(t *testing.T) {
		_, err := db.ExecContext(ctx,
			`INSERT INTO otp_challenges (id, phone_e164, code_hash, attempts, max_attempts, expires_at, resend_after)
			 VALUES ($1, '+919876543210', 'h', 6, 5, $2, $2)`,
			uuid.Must(uuid.NewV7()), now)
		require.Error(t, err)
	})
}
