package travelprofile

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/adi6859/travel-swipe-backend/pkg/clock"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
)

type fakeTx struct{}

func (fakeTx) RunInTx(ctx context.Context, fn func(ctx context.Context) error) error { return fn(ctx) }

type fakeStore struct {
	interests []Interest
	profiles  map[uuid.UUID]Profile
	users     map[uuid.UUID]bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		interests: []Interest{
			{"trekking", "Trekking", "outdoors"},
			{"beaches", "Beaches", "outdoors"},
			{"street_food", "Street food", "food_drink"},
			{"heritage", "Heritage sites", "culture"},
		},
		profiles: map[uuid.UUID]Profile{},
		users:    map[uuid.UUID]bool{},
	}
}

func (f *fakeStore) addUser() uuid.UUID {
	id := uuid.Must(uuid.NewV7())
	f.users[id] = true
	return id
}

func (f *fakeStore) ActiveInterests(context.Context) ([]Interest, error) { return f.interests, nil }

func (f *fakeStore) Get(_ context.Context, userID uuid.UUID) (Profile, error) {
	if p, ok := f.profiles[userID]; ok {
		return p, nil
	}
	return emptyProfile(userID), nil
}

func (f *fakeStore) Save(_ context.Context, p Profile, at time.Time) error {
	if !f.users[p.UserID] {
		return ErrUserNotFound
	}
	p.UpdatedAt = &at
	f.profiles[p.UserID] = p
	return nil
}

func newTestService() (*Service, *fakeStore, *clock.MockClock) {
	store := newFakeStore()
	clk := &clock.MockClock{Current: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)}
	return NewService(store, fakeTx{}, clk), store, clk
}

func ptr(s string) *string { return &s }

func TestGetReturnsEmptyProfileBeforeFirstSave(t *testing.T) {
	svc, store, _ := newTestService()
	id := store.addUser()

	p, err := svc.Get(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, []string{}, p.TravelStyles)
	require.Equal(t, []string{}, p.Interests)
	require.Equal(t, []string{}, p.Languages)
	require.Nil(t, p.UpdatedAt)
	require.False(t, p.Complete())
}

func TestReplaceNormalizesAndDeduplicates(t *testing.T) {
	svc, store, clk := newTestService()
	id := store.addUser()

	p, err := svc.Replace(context.Background(), id, Input{
		TravelStyles: []string{" Backpacker", "backpacker", "ADVENTURE"},
		Interests:    []string{"trekking", "Beaches", "trekking", "street_food"},
		Languages:    []string{"EN", "hi", "en"},
		BudgetBand:   ptr(" Budget "),
		Diet:         ptr("JAIN"),
	})
	require.NoError(t, err)
	require.Equal(t, []string{"backpacker", "adventure"}, p.TravelStyles)
	require.Equal(t, []string{"trekking", "beaches", "street_food"}, p.Interests)
	require.Equal(t, []string{"en", "hi"}, p.Languages)
	require.Equal(t, "budget", *p.BudgetBand)
	require.Equal(t, "jain", *p.Diet)
	require.Nil(t, p.Pace)
	require.Equal(t, clk.Now(), *p.UpdatedAt)
	require.True(t, p.Complete())
}

func TestReplaceIsFullReplacement(t *testing.T) {
	svc, store, _ := newTestService()
	id := store.addUser()
	ctx := context.Background()

	_, err := svc.Replace(ctx, id, Input{Interests: []string{"trekking"}, Pace: ptr("packed")})
	require.NoError(t, err)

	p, err := svc.Replace(ctx, id, Input{Languages: []string{"ta"}})
	require.NoError(t, err)
	require.Equal(t, []string{}, p.Interests, "omitted list is cleared")
	require.Nil(t, p.Pace, "omitted scalar is cleared")
	require.Equal(t, []string{"ta"}, p.Languages)
}

func TestReplaceAcceptsEmptyInput(t *testing.T) {
	svc, store, _ := newTestService()
	id := store.addUser()

	p, err := svc.Replace(context.Background(), id, Input{})
	require.NoError(t, err)
	require.Equal(t, []string{}, p.TravelStyles)
	require.NotNil(t, p.UpdatedAt)
}

func TestReplaceValidation(t *testing.T) {
	tooManyLangs := []string{"en", "hi", "bn", "te", "mr", "ta", "ur", "gu", "kn"}

	cases := []struct {
		name  string
		in    Input
		field string
		msg   string
	}{
		{"unknown style", Input{TravelStyles: []string{"party"}}, "travel_styles", "not allowed"},
		{"too many styles", Input{TravelStyles: []string{"backpacker", "comfort", "luxury", "slow"}}, "travel_styles", "at most 3"},
		{"empty style string", Input{TravelStyles: []string{" "}}, "travel_styles", "not allowed"},
		{"unknown interest", Input{Interests: []string{"skydiving"}}, "interests", "not allowed"},
		{"inactive or unseeded interest", Input{Interests: []string{"trekking", "camping"}}, "interests", "not allowed"},
		{"unknown language", Input{Languages: []string{"xx"}}, "languages", "not allowed"},
		{"three letter language", Input{Languages: []string{"eng"}}, "languages", "not allowed"},
		{"too many languages", Input{Languages: tooManyLangs}, "languages", "at most 8"},
		{"bad budget", Input{BudgetBand: ptr("cheap")}, "budget_band", "not an allowed value"},
		{"bad group size", Input{GroupSize: ptr("huge")}, "group_size", "not an allowed value"},
		{"bad pace", Input{Pace: ptr("fast")}, "pace", "not an allowed value"},
		{"bad smoking", Input{Smoking: ptr("yes")}, "smoking", "not an allowed value"},
		{"bad drinking", Input{Drinking: ptr("")}, "drinking", "not an allowed value"},
		{"bad diet", Input{Diet: ptr("keto")}, "diet", "not an allowed value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, _ := newTestService()
			_, err := svc.Replace(context.Background(), store.addUser(), tc.in)
			require.Equal(t, apperrors.CodeInvalid, apperrors.CodeOf(err))
			details := apperrors.Details(err)
			require.Contains(t, details, tc.field)
			require.Contains(t, details[tc.field], tc.msg)
		})
	}
}

func TestReplaceInterestCapCountsDistinctValues(t *testing.T) {
	svc, store, _ := newTestService()
	store.interests = nil
	var many []string
	for i := 0; i < 11; i++ {
		slug := "interest_" + strings.Repeat("a", i+1)
		store.interests = append(store.interests, Interest{Slug: slug, Label: slug, Category: "leisure"})
		many = append(many, slug)
	}

	_, err := svc.Replace(context.Background(), store.addUser(), Input{Interests: many})
	require.Contains(t, apperrors.Details(err)["interests"], "at most 10")

	_, err = svc.Replace(context.Background(), store.addUser(), Input{Interests: append(many[:10:10], many[0])})
	require.NoError(t, err, "duplicates do not count toward the cap")
}

func TestReplaceReportsAllInvalidFieldsTogether(t *testing.T) {
	svc, store, _ := newTestService()
	_, err := svc.Replace(context.Background(), store.addUser(), Input{
		TravelStyles: []string{"nope"}, Languages: []string{"zz"}, Pace: ptr("x"), Diet: ptr("y"),
	})
	require.Len(t, apperrors.Details(err), 4)
}

func TestReplaceUnknownUser(t *testing.T) {
	svc, _, _ := newTestService()
	_, err := svc.Replace(context.Background(), uuid.Must(uuid.NewV7()), Input{})
	require.Equal(t, apperrors.CodeNotFound, apperrors.CodeOf(err))
}

func TestOptionsListEveryVocabulary(t *testing.T) {
	svc, _, _ := newTestService()
	o, err := svc.Options(context.Background())
	require.NoError(t, err)
	require.Len(t, o.Interests, 4)
	for name, opts := range map[string][]Option{
		"travel_styles": o.TravelStyles, "languages": o.Languages, "budget_bands": o.BudgetBands,
		"group_sizes": o.GroupSizes, "paces": o.Paces, "smoking": o.Smoking, "drinking": o.Drinking, "diets": o.Diets,
	} {
		require.NotEmpty(t, opts, name)
	}
}

func TestCompleteRules(t *testing.T) {
	base := Profile{
		TravelStyles: []string{"slow"},
		Interests:    []string{"a", "b", "c"},
		Languages:    []string{"en"},
		BudgetBand:   ptr("budget"),
	}
	require.True(t, base.Complete())

	p := base
	p.Interests = []string{"a", "b"}
	require.False(t, p.Complete(), "needs at least three interests")

	p = base
	p.BudgetBand = nil
	require.False(t, p.Complete(), "needs a budget band")

	p = base
	p.Languages = []string{}
	require.False(t, p.Complete(), "needs a language")

	p = base
	p.TravelStyles = nil
	require.False(t, p.Complete(), "needs a travel style")
}
