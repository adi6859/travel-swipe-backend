package users

import (
	"context"
	"strings"
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

func TestRepositoryUpdateProfileSetAndClear(t *testing.T) {
	db := dbtest.Open(t)
	repo := NewRepository(db)
	ctx := context.Background()
	u := newUser("+919876543210")
	require.NoError(t, repo.Create(ctx, u))

	var ch ProfileChanges
	ch.set("display_name", "Asha")
	ch.set("bio", "Line one\nLine two")
	ch.set("avatar_url", "https://cdn.example.com/a.jpg")
	ch.set("avatar_meta", `{"url":"https://cdn.example.com/a.jpg","width":512,"height":512,"mime_type":"image/jpeg","size_bytes":2048}`)
	ch.set("date_of_birth", "1998-04-15")
	ch.set("gender", "female")
	ch.set("home_city", "Bengaluru")
	ch.set("country_code", "IN")
	at := time.Now().UTC().Truncate(time.Microsecond)

	p, err := repo.UpdateProfile(ctx, u.ID, ch, at)
	require.NoError(t, err)
	require.Equal(t, "Asha", p.DisplayName)
	require.Equal(t, "Line one\nLine two", p.Bio)
	require.Equal(t, "https://cdn.example.com/a.jpg", *p.AvatarURL)
	require.JSONEq(t, `{"url":"https://cdn.example.com/a.jpg","width":512,"height":512,"mime_type":"image/jpeg","size_bytes":2048}`, string(p.AvatarMeta))
	require.Equal(t, "1998-04-15", p.DateOfBirth.Format("2006-01-02"))
	require.Equal(t, "female", *p.Gender)
	require.Equal(t, "IN", *p.CountryCode)
	require.True(t, at.Equal(p.UpdatedAt))

	got, err := repo.GetProfile(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, p, got)

	var clear ProfileChanges
	clear.set("avatar_url", nil)
	clear.set("avatar_meta", nil)
	clear.set("date_of_birth", nil)
	clear.set("gender", nil)
	p, err = repo.UpdateProfile(ctx, u.ID, clear, at)
	require.NoError(t, err)
	require.Nil(t, p.AvatarURL)
	require.Nil(t, p.AvatarMeta)
	require.Nil(t, p.DateOfBirth)
	require.Nil(t, p.Gender)
	require.Equal(t, "Asha", p.DisplayName, "unlisted columns unchanged")
}

func TestRepositoryUpdateProfileUnknownUser(t *testing.T) {
	db := dbtest.Open(t)
	repo := NewRepository(db)
	var ch ProfileChanges
	ch.set("bio", "x")

	_, err := repo.UpdateProfile(context.Background(), uuid.New(), ch, time.Now())
	require.ErrorIs(t, err, ErrNotFound)
	_, err = repo.GetProfile(context.Background(), uuid.New())
	require.ErrorIs(t, err, ErrNotFound)
}

func TestRepositoryProfileConstraintsBackstopValidation(t *testing.T) {
	db := dbtest.Open(t)
	repo := NewRepository(db)
	ctx := context.Background()
	u := newUser("+919876543210")
	require.NoError(t, repo.Create(ctx, u))

	for col, val := range map[string]any{"gender": "other", "country_code": "in", "display_name": strings.Repeat("a", 61)} {
		var ch ProfileChanges
		ch.set(col, val)
		_, err := repo.UpdateProfile(ctx, u.ID, ch, time.Now())
		require.Error(t, err, col)
	}
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
