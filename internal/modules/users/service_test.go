package users

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/adi6859/travel-swipe-backend/pkg/clock"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
	"github.com/adi6859/travel-swipe-backend/pkg/patch"
)

var testNow = time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)

// fakeStore applies ProfileChanges to an in-memory profile the way the SQL does.
type fakeStore struct {
	users    map[uuid.UUID]User
	profiles map[uuid.UUID]Profile
	updates  int
}

func newFakeStore() *fakeStore {
	return &fakeStore{users: map[uuid.UUID]User{}, profiles: map[uuid.UUID]Profile{}}
}

func (f *fakeStore) add() uuid.UUID {
	id := uuid.Must(uuid.NewV7())
	f.users[id] = User{ID: id, PhoneE164: "+919876543210", Status: StatusActive, CreatedAt: testNow}
	f.profiles[id] = Profile{UserID: id, UpdatedAt: testNow}
	return id
}

func (f *fakeStore) FindByID(_ context.Context, id uuid.UUID) (User, error) {
	u, ok := f.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (f *fakeStore) GetProfile(_ context.Context, id uuid.UUID) (Profile, error) {
	p, ok := f.profiles[id]
	if !ok {
		return Profile{}, ErrNotFound
	}
	return p, nil
}

func (f *fakeStore) UpdateProfile(_ context.Context, id uuid.UUID, ch ProfileChanges, at time.Time) (Profile, error) {
	p, ok := f.profiles[id]
	if !ok {
		return Profile{}, ErrNotFound
	}
	f.updates++
	str := func(v any) *string {
		if v == nil {
			return nil
		}
		s := v.(string)
		return &s
	}
	for i, col := range ch.columns {
		v := ch.values[i]
		switch col {
		case "display_name":
			p.DisplayName = v.(string)
		case "bio":
			p.Bio = v.(string)
		case "avatar_url":
			p.AvatarURL = str(v)
		case "avatar_meta":
			if v == nil {
				p.AvatarMeta = nil
			} else {
				p.AvatarMeta = []byte(v.(string))
			}
		case "date_of_birth":
			if v == nil {
				p.DateOfBirth = nil
			} else {
				d, _ := time.Parse(dateOfBirthStyle, v.(string))
				p.DateOfBirth = &d
			}
		case "gender":
			p.Gender = str(v)
		case "home_city":
			p.HomeCity = str(v)
		case "country_code":
			p.CountryCode = str(v)
		default:
			panic("unexpected column " + col)
		}
	}
	p.UpdatedAt = at
	f.profiles[id] = p
	return p, nil
}

func newTestService() (*Service, *fakeStore) {
	store := newFakeStore()
	return NewService(store, &clock.MockClock{Current: testNow}), store
}

var validAvatar = Avatar{URL: "https://cdn.example.com/a.jpg", Width: 512, Height: 512, MimeType: "image/jpeg", SizeBytes: 2048}

func TestGetMe(t *testing.T) {
	t.Parallel()
	svc, store := newTestService()
	id := store.add()

	me, err := svc.GetMe(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, id, me.User.ID)
	require.Equal(t, id, me.Profile.UserID)

	_, err = svc.GetMe(context.Background(), uuid.New())
	require.Equal(t, apperrors.CodeNotFound, apperrors.CodeOf(err))
}

func TestUpdateMeSetsAllFields(t *testing.T) {
	t.Parallel()
	svc, store := newTestService()
	id := store.add()

	me, err := svc.UpdateMe(context.Background(), id, ProfileUpdate{
		DisplayName: patch.Of("  Asha  "),
		Bio:         patch.Of("Backpacker.\nMountains > beaches."),
		Avatar:      patch.Of(validAvatar),
		DateOfBirth: patch.Of("1998-04-15"),
		Gender:      patch.Of("female"),
		HomeCity:    patch.Of("Bengaluru"),
		CountryCode: patch.Of("in"),
	})
	require.NoError(t, err)

	p := me.Profile
	require.Equal(t, "Asha", p.DisplayName, "trimmed")
	require.Equal(t, "Backpacker.\nMountains > beaches.", p.Bio)
	require.Equal(t, validAvatar.URL, *p.AvatarURL)
	var meta Avatar
	require.NoError(t, json.Unmarshal(p.AvatarMeta, &meta))
	require.Equal(t, validAvatar, meta)
	require.Equal(t, "1998-04-15", p.DateOfBirth.Format(dateOfBirthStyle))
	require.Equal(t, "female", *p.Gender)
	require.Equal(t, "Bengaluru", *p.HomeCity)
	require.Equal(t, "IN", *p.CountryCode, "upper-cased")
	require.Equal(t, testNow, p.UpdatedAt)
}

func TestUpdateMeNullClearsAndAbsentKeeps(t *testing.T) {
	t.Parallel()
	svc, store := newTestService()
	id := store.add()
	ctx := context.Background()

	_, err := svc.UpdateMe(ctx, id, ProfileUpdate{
		DisplayName: patch.Of("Asha"),
		Avatar:      patch.Of(validAvatar),
		HomeCity:    patch.Of("Pune"),
	})
	require.NoError(t, err)

	me, err := svc.UpdateMe(ctx, id, ProfileUpdate{Avatar: patch.Null[Avatar](), HomeCity: patch.Null[string]()})
	require.NoError(t, err)
	require.Nil(t, me.Profile.AvatarURL)
	require.Nil(t, me.Profile.AvatarMeta)
	require.Nil(t, me.Profile.HomeCity)
	require.Equal(t, "Asha", me.Profile.DisplayName, "absent field untouched")
}

func TestUpdateMeRejectsEmptyPatch(t *testing.T) {
	t.Parallel()
	svc, store := newTestService()
	id := store.add()

	_, err := svc.UpdateMe(context.Background(), id, ProfileUpdate{})
	require.Equal(t, apperrors.CodeInvalid, apperrors.CodeOf(err))
	require.Zero(t, store.updates)
}

func TestUpdateMeValidation(t *testing.T) {
	t.Parallel()
	long := func(n int) string {
		b := make([]rune, n)
		for i := range b {
			b[i] = 'अ' // multi-byte: limits count characters, not bytes
		}
		return string(b)
	}
	badAvatar := func(mut func(*Avatar)) patch.Field[Avatar] {
		a := validAvatar
		mut(&a)
		return patch.Of(a)
	}

	cases := []struct {
		name  string
		upd   ProfileUpdate
		field string
	}{
		{"blank display name", ProfileUpdate{DisplayName: patch.Of("   ")}, "display_name"},
		{"display name too long", ProfileUpdate{DisplayName: patch.Of(long(61))}, "display_name"},
		{"display name control char", ProfileUpdate{DisplayName: patch.Of("A\x00sha")}, "display_name"},
		{"display name newline", ProfileUpdate{DisplayName: patch.Of("A\nsha")}, "display_name"},
		{"bio too long", ProfileUpdate{Bio: patch.Of(long(501))}, "bio"},
		{"bio tab", ProfileUpdate{Bio: patch.Of("a\tb")}, "bio"},
		{"avatar http", ProfileUpdate{Avatar: badAvatar(func(a *Avatar) { a.URL = "http://x.com/a.jpg" })}, "avatar"},
		{"avatar javascript", ProfileUpdate{Avatar: badAvatar(func(a *Avatar) { a.URL = "javascript:alert(1)" })}, "avatar"},
		{"avatar credentials", ProfileUpdate{Avatar: badAvatar(func(a *Avatar) { a.URL = "https://u:p@x.com/a.jpg" })}, "avatar"},
		{"avatar mime", ProfileUpdate{Avatar: badAvatar(func(a *Avatar) { a.MimeType = "image/gif" })}, "avatar"},
		{"avatar dims", ProfileUpdate{Avatar: badAvatar(func(a *Avatar) { a.Width = 0 })}, "avatar"},
		{"avatar size", ProfileUpdate{Avatar: badAvatar(func(a *Avatar) { a.SizeBytes = 11 << 20 })}, "avatar"},
		{"dob format", ProfileUpdate{DateOfBirth: patch.Of("15-04-1998")}, "date_of_birth"},
		{"dob impossible", ProfileUpdate{DateOfBirth: patch.Of("1998-02-30")}, "date_of_birth"},
		{"dob under 18", ProfileUpdate{DateOfBirth: patch.Of("2008-10-03")}, "date_of_birth"},
		{"dob future", ProfileUpdate{DateOfBirth: patch.Of("2030-01-01")}, "date_of_birth"},
		{"dob implausible", ProfileUpdate{DateOfBirth: patch.Of("1890-01-01")}, "date_of_birth"},
		{"gender", ProfileUpdate{Gender: patch.Of("other")}, "gender"},
		{"country", ProfileUpdate{CountryCode: patch.Of("IND")}, "country_code"},
		{"home city blank", ProfileUpdate{HomeCity: patch.Of("")}, "home_city"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc, store := newTestService()
			id := store.add()
			_, err := svc.UpdateMe(context.Background(), id, tc.upd)
			require.Equal(t, apperrors.CodeInvalid, apperrors.CodeOf(err))
			require.Contains(t, apperrors.Details(err), tc.field)
			require.Zero(t, store.updates, "nothing is written on validation failure")
		})
	}
}

func TestUpdateMeReportsAllInvalidFieldsAtOnce(t *testing.T) {
	t.Parallel()
	svc, store := newTestService()
	id := store.add()

	_, err := svc.UpdateMe(context.Background(), id, ProfileUpdate{
		Gender:      patch.Of("x"),
		CountryCode: patch.Of("x"),
		DisplayName: patch.Of("ok"),
	})
	require.Len(t, apperrors.Details(err), 2)
}

func TestAgeBoundary(t *testing.T) {
	t.Parallel()
	svc, store := newTestService()
	id := store.add()

	_, err := svc.UpdateMe(context.Background(), id, ProfileUpdate{DateOfBirth: patch.Of("2008-10-02")})
	require.NoError(t, err, "18th birthday today is allowed")

	_, err = svc.UpdateMe(context.Background(), id, ProfileUpdate{DateOfBirth: patch.Of("2008-10-03")})
	require.Error(t, err, "one day short of 18")
}
