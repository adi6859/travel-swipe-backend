package users

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/adi6859/travel-swipe-backend/internal/platform/database"
	"github.com/adi6859/travel-swipe-backend/internal/platform/database/dbtest"
)

func newUser(phone string) User {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return User{ID: uuid.Must(uuid.NewV7()), PhoneE164: phone, Status: StatusActive,
		PhoneVerifiedAt: now, CreatedAt: now, UpdatedAt: now}
}

func TestRepositoryCreateFindAndDuplicate(t *testing.T) {
	db := dbtest.Open(t)
	repo := NewRepository(db)
	ctx := context.Background()

	u := newUser("+919876543210")
	require.NoError(t, database.NewTxManager(db).RunInTx(ctx, func(ctx context.Context) error {
		return repo.Create(ctx, u)
	}))

	got, err := repo.FindByPhone(ctx, u.PhoneE164)
	require.NoError(t, err)
	require.Equal(t, u.ID, got.ID)
	require.Equal(t, StatusActive, got.Status)
	require.Nil(t, got.LastLoginAt)

	var profiles int
	require.NoError(t, db.GetContext(ctx, &profiles, `SELECT count(*) FROM user_profiles WHERE user_id = $1`, u.ID))
	require.Equal(t, 1, profiles, "an empty profile is created with the user")

	require.ErrorIs(t, repo.Create(ctx, newUser(u.PhoneE164)), ErrPhoneTaken)

	_, err = repo.FindByPhone(ctx, "+919000000000")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = repo.FindByID(ctx, uuid.New())
	require.ErrorIs(t, err, ErrNotFound)
}

func TestRepositoryRecordLogin(t *testing.T) {
	db := dbtest.Open(t)
	repo := NewRepository(db)
	ctx := context.Background()
	u := newUser("+919876543210")
	require.NoError(t, repo.Create(ctx, u))

	at := time.Now().UTC().Truncate(time.Microsecond)
	require.NoError(t, repo.RecordLogin(ctx, u.ID, at))
	got, err := repo.FindByID(ctx, u.ID)
	require.NoError(t, err)
	require.NotNil(t, got.LastLoginAt)
	require.True(t, at.Equal(*got.LastLoginAt))
}

func TestRepositoryTransactionRollsBackBothRows(t *testing.T) {
	db := dbtest.Open(t)
	repo := NewRepository(db)
	ctx := context.Background()
	u := newUser("+919876543210")

	err := database.NewTxManager(db).RunInTx(ctx, func(ctx context.Context) error {
		require.NoError(t, repo.Create(ctx, u))
		return context.Canceled
	})
	require.ErrorIs(t, err, context.Canceled)

	_, err = repo.FindByID(ctx, u.ID)
	require.ErrorIs(t, err, ErrNotFound)
}
