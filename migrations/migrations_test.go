package migrations_test

import (
	"context"
	"io/fs"
	"strings"
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
	require.Contains(t, names, "00002_travel_profile.sql")
	require.Contains(t, names, "00003_trip_catalogue.sql")
}

func TestCatalogueConstraints(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()

	insertSource := func(basis string, autoPublish bool) (uuid.UUID, error) {
		id := uuid.Must(uuid.NewV7())
		_, err := db.ExecContext(ctx, `
			INSERT INTO ingest_sources (id, source_key, display_name, base_url, rights_basis, auto_publish, evidence_ref)
			VALUES ($1, $2, 'S', 'https://example.com', $3, $4, 'e')`,
			id, "s_"+strings.ReplaceAll(id.String()[:8], "-", ""), basis, autoPublish)
		return id, err
	}
	_, err := insertSource("research_only", true)
	require.Error(t, err, "research_only sources can never auto-publish")
	sourceID, err := insertSource("contract", true)
	require.NoError(t, err)

	providerID := uuid.Must(uuid.NewV7())
	_, err = db.ExecContext(ctx, `INSERT INTO providers (id, slug, name) VALUES ($1, 'acme', 'Acme')`, providerID)
	require.NoError(t, err)

	insertTrip := func(bookingURL, status string, publishedAt *time.Time) error {
		id := uuid.Must(uuid.NewV7())
		_, err := db.ExecContext(ctx, `
			INSERT INTO trips (id, provider_id, source_id, slug, title, destination, country_code, duration_days,
			    duration_nights, booking_url, status, published_at)
			VALUES ($1, $2, $3, $4, 'T', 'D', 'IN', 2, 1, $5, $6, $7)`,
			id, providerID, sourceID, "t-"+id.String(), bookingURL, status, publishedAt)
		return err
	}
	now := time.Now()
	require.NoError(t, insertTrip("https://example.com/book", "published", &now))
	require.Error(t, insertTrip("http://example.com/book", "draft", nil), "booking links must be https")
	require.Error(t, insertTrip("https://example.com/book", "published", nil), "published trips need published_at")
	require.Error(t, insertTrip("https://example.com/book", "live", nil))
}

func TestMigrationsResetUp(t *testing.T) {
	db := dbtest.Open(t)
	ctx := context.Background()

	require.NoError(t, migrations.RunWithDB(ctx, db.DB, "reset"), "every down migration runs cleanly")
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
