package travelprofile

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"github.com/adi6859/travel-swipe-backend/internal/platform/database"
	"github.com/adi6859/travel-swipe-backend/internal/platform/database/dbtest"
)

func insertUser(t *testing.T, db *sqlx.DB, phone string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := db.Exec(`INSERT INTO users (id, phone_e164, phone_verified_at) VALUES ($1, $2, now())`, id, phone)
	require.NoError(t, err)
	return id
}

func save(t *testing.T, db *sqlx.DB, repo *Repository, p Profile, at time.Time) error {
	t.Helper()
	return database.NewTxManager(db).RunInTx(context.Background(), func(ctx context.Context) error {
		return repo.Save(ctx, p, at)
	})
}

func TestRepositoryGetWithoutProfileReturnsEmpty(t *testing.T) {
	db := dbtest.Open(t)
	repo := NewRepository(db)
	id := insertUser(t, db, "+919800000001")

	p, err := repo.Get(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, emptyProfile(id), p)
}

func TestRepositorySaveGetAndReplace(t *testing.T) {
	db := dbtest.Open(t)
	repo := NewRepository(db)
	ctx := context.Background()
	id := insertUser(t, db, "+919800000002")
	at := time.Now().UTC().Truncate(time.Microsecond)

	require.NoError(t, save(t, db, repo, Profile{
		UserID:       id,
		TravelStyles: []string{"slow", "backpacker"},
		Interests:    []string{"street_food", "trekking", "heritage"},
		Languages:    []string{"en", "kn"},
		BudgetBand:   ptr("mid_range"),
		Diet:         ptr("eggetarian"),
	}, at))

	p, err := repo.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, []string{"slow", "backpacker"}, p.TravelStyles, "style order is preserved")
	require.Equal(t, []string{"trekking", "heritage", "street_food"}, p.Interests, "interests come back in catalogue order")
	require.Equal(t, []string{"en", "kn"}, p.Languages)
	require.Equal(t, "mid_range", *p.BudgetBand)
	require.Equal(t, "eggetarian", *p.Diet)
	require.Nil(t, p.Pace)
	require.True(t, at.Equal(*p.UpdatedAt))

	later := at.Add(time.Minute)
	require.NoError(t, save(t, db, repo, Profile{
		UserID: id, TravelStyles: []string{}, Interests: []string{"beaches"}, Languages: []string{}, Pace: ptr("relaxed"),
	}, later))

	p, err = repo.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, []string{}, p.TravelStyles)
	require.Equal(t, []string{"beaches"}, p.Interests, "old interests are replaced")
	require.Nil(t, p.BudgetBand)
	require.Equal(t, "relaxed", *p.Pace)
	require.True(t, later.Equal(*p.UpdatedAt))

	require.NoError(t, save(t, db, repo, Profile{UserID: id}, later), "nil slices are stored as empty arrays")
	p, err = repo.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, []string{}, p.Interests)
}

func TestRepositorySaveUnknownUserAndInterest(t *testing.T) {
	db := dbtest.Open(t)
	repo := NewRepository(db)
	now := time.Now().UTC()

	err := save(t, db, repo, Profile{UserID: uuid.Must(uuid.NewV7())}, now)
	require.ErrorIs(t, err, ErrUserNotFound)

	id := insertUser(t, db, "+919800000003")
	err = save(t, db, repo, Profile{UserID: id, Interests: []string{"trekking", "skydiving"}}, now)
	require.ErrorIs(t, err, ErrUnknownInterest)

	p, err := repo.Get(context.Background(), id)
	require.NoError(t, err)
	require.Nil(t, p.UpdatedAt, "failed save rolled back the profile row too")
}

func TestRepositoryActiveInterestsExcludesRetired(t *testing.T) {
	db := dbtest.Open(t)
	repo := NewRepository(db)
	ctx := context.Background()

	var seeded int
	require.NoError(t, db.Get(&seeded, `SELECT count(*) FROM interests`))
	all, err := repo.ActiveInterests(ctx)
	require.NoError(t, err)
	require.Len(t, all, seeded)
	require.Equal(t, "trekking", all[0].Slug)

	_, err = db.Exec(`UPDATE interests SET active = false WHERE slug = 'nightlife'`)
	require.NoError(t, err)
	active, err := repo.ActiveInterests(ctx)
	require.NoError(t, err)
	require.Len(t, active, seeded-1)
	for _, i := range active {
		require.NotEqual(t, "nightlife", i.Slug)
	}
}

func TestRepositoryDeletingUserCascades(t *testing.T) {
	db := dbtest.Open(t)
	repo := NewRepository(db)
	id := insertUser(t, db, "+919800000004")
	require.NoError(t, save(t, db, repo, Profile{UserID: id, Interests: []string{"cafes"}}, time.Now().UTC()))

	_, err := db.Exec(`DELETE FROM users WHERE id = $1`, id)
	require.NoError(t, err)

	var n int
	require.NoError(t, db.Get(&n, `SELECT (SELECT count(*) FROM travel_profiles) + (SELECT count(*) FROM travel_profile_interests)`))
	require.Zero(t, n)
}

// The Go vocabularies and the database CHECK constraints must agree: every Go
// value is accepted by the database, and values outside them are rejected.
func TestVocabularyMatchesDatabaseConstraints(t *testing.T) {
	db := dbtest.Open(t)
	repo := NewRepository(db)
	now := time.Now().UTC()
	id := insertUser(t, db, "+919800000005")

	scalar := func(set func(p *Profile, v *string), opts []Option) {
		for _, o := range opts {
			p := Profile{UserID: id}
			set(&p, ptr(o.Value))
			require.NoError(t, save(t, db, repo, p, now), o.Value)
		}
		p := Profile{UserID: id}
		set(&p, ptr("not_a_value"))
		require.Error(t, save(t, db, repo, p, now))
	}
	scalar(func(p *Profile, v *string) { p.BudgetBand = v }, budgetBandOptions)
	scalar(func(p *Profile, v *string) { p.GroupSize = v }, groupSizeOptions)
	scalar(func(p *Profile, v *string) { p.Pace = v }, paceOptions)
	scalar(func(p *Profile, v *string) { p.Smoking = v }, habitOptions)
	scalar(func(p *Profile, v *string) { p.Drinking = v }, habitOptions)
	scalar(func(p *Profile, v *string) { p.Diet = v }, dietOptions)

	for _, o := range travelStyleOptions {
		require.NoError(t, save(t, db, repo, Profile{UserID: id, TravelStyles: []string{o.Value}}, now), o.Value)
	}
	require.Error(t, save(t, db, repo, Profile{UserID: id, TravelStyles: []string{"party"}}, now))
	require.Error(t, save(t, db, repo, Profile{UserID: id, TravelStyles: []string{"slow", "comfort", "luxury", "adventure"}}, now),
		"database caps travel styles at 3")

	for _, o := range languageOptions {
		require.NoError(t, save(t, db, repo, Profile{UserID: id, Languages: []string{o.Value}}, now), o.Value)
	}
	require.Error(t, save(t, db, repo, Profile{UserID: id, Languages: []string{"eng"}}, now))
	require.Error(t, save(t, db, repo, Profile{UserID: id, Languages: []string{"en", "hi", "bn", "te", "mr", "ta", "ur", "gu", "kn"}}, now),
		"database caps languages at 8")
}
