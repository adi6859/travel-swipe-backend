package users

import (
	"context"
	"encoding/json"
	stdErrors "errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/adi6859/travel-swipe-backend/pkg/clock"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
	"github.com/adi6859/travel-swipe-backend/pkg/patch"
	"github.com/adi6859/travel-swipe-backend/pkg/require"
)

const (
	maxDisplayName   = 60
	maxBio           = 500
	maxHomeCity      = 100
	maxAvatarURL     = 2048
	maxAvatarPixels  = 10000
	maxAvatarBytes   = 10 << 20
	MinimumAge       = 18
	maximumAge       = 120
	dateOfBirthStyle = "2006-01-02"
)

var (
	countryCode = regexp.MustCompile(`^[A-Z]{2}$`)
	genders     = map[string]bool{"male": true, "female": true, "non_binary": true, "prefer_not_to_say": true}
	avatarMimes = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true, "image/heic": true}
)

// ProfileUpdate is a partial profile update. Absent fields are left unchanged;
// null clears a field.
type ProfileUpdate struct {
	DisplayName patch.Field[string]
	Bio         patch.Field[string]
	Avatar      patch.Field[Avatar]
	DateOfBirth patch.Field[string]
	Gender      patch.Field[string]
	HomeCity    patch.Field[string]
	CountryCode patch.Field[string]
}

type Store interface {
	FindByID(ctx context.Context, id uuid.UUID) (User, error)
	GetProfile(ctx context.Context, userID uuid.UUID) (Profile, error)
	UpdateProfile(ctx context.Context, userID uuid.UUID, changes ProfileChanges, at time.Time) (Profile, error)
}

type Service struct {
	store Store
	clock clock.Clock
}

func NewService(store Store, clk clock.Clock) *Service {
	require.AllNotNil("users service", map[string]any{"store": store, "clock": clk})
	return &Service{store: store, clock: clk}
}

func (s *Service) GetMe(ctx context.Context, userID uuid.UUID) (Me, error) {
	user, err := s.store.FindByID(ctx, userID)
	if err != nil {
		return Me{}, mapStoreErr(err, "load user")
	}
	profile, err := s.store.GetProfile(ctx, userID)
	if err != nil {
		return Me{}, mapStoreErr(err, "load profile")
	}
	return Me{User: user, Profile: profile}, nil
}

func (s *Service) UpdateMe(ctx context.Context, userID uuid.UUID, upd ProfileUpdate) (Me, error) {
	now := s.clock.Now()
	changes, err := s.validate(upd, now)
	if err != nil {
		return Me{}, err
	}
	user, err := s.store.FindByID(ctx, userID)
	if err != nil {
		return Me{}, mapStoreErr(err, "load user")
	}
	profile, err := s.store.UpdateProfile(ctx, userID, changes, now)
	if err != nil {
		return Me{}, mapStoreErr(err, "update profile")
	}
	return Me{User: user, Profile: profile}, nil
}

func (s *Service) validate(upd ProfileUpdate, now time.Time) (ProfileChanges, error) {
	var (
		changes ProfileChanges
		fields  = map[string]string{}
	)

	if f := upd.DisplayName; f.Set {
		if f.Null {
			changes.set("display_name", "")
		} else if v, msg := cleanText(f.Value, maxDisplayName, false); msg != "" {
			fields["display_name"] = msg
		} else {
			changes.set("display_name", v)
		}
	}

	if f := upd.Bio; f.Set {
		if f.Null {
			changes.set("bio", "")
		} else if v, msg := cleanText(f.Value, maxBio, true); msg != "" {
			fields["bio"] = msg
		} else {
			changes.set("bio", v)
		}
	}

	if f := upd.Avatar; f.Set {
		if f.Null {
			changes.set("avatar_url", nil)
			changes.set("avatar_meta", nil)
		} else if msg := validateAvatar(f.Value); msg != "" {
			fields["avatar"] = msg
		} else {
			meta, _ := json.Marshal(f.Value)
			changes.set("avatar_url", f.Value.URL)
			changes.set("avatar_meta", string(meta))
		}
	}

	if f := upd.DateOfBirth; f.Set {
		if f.Null {
			changes.set("date_of_birth", nil)
		} else if dob, msg := validateDateOfBirth(f.Value, now); msg != "" {
			fields["date_of_birth"] = msg
		} else {
			changes.set("date_of_birth", dob)
		}
	}

	if f := upd.Gender; f.Set {
		if f.Null {
			changes.set("gender", nil)
		} else if !genders[f.Value] {
			fields["gender"] = "must be one of: male, female, non_binary, prefer_not_to_say"
		} else {
			changes.set("gender", f.Value)
		}
	}

	if f := upd.HomeCity; f.Set {
		if f.Null {
			changes.set("home_city", nil)
		} else if v, msg := cleanText(f.Value, maxHomeCity, false); msg != "" {
			fields["home_city"] = msg
		} else {
			changes.set("home_city", v)
		}
	}

	if f := upd.CountryCode; f.Set {
		if f.Null {
			changes.set("country_code", nil)
		} else if v := strings.ToUpper(strings.TrimSpace(f.Value)); !countryCode.MatchString(v) {
			fields["country_code"] = "must be an ISO 3166-1 alpha-2 code, e.g. IN"
		} else {
			changes.set("country_code", v)
		}
	}

	if len(fields) > 0 {
		return ProfileChanges{}, apperrors.InvalidFields("request validation failed", fields)
	}
	if changes.Empty() {
		return ProfileChanges{}, apperrors.Invalid("no profile fields to update")
	}
	return changes, nil
}

// cleanText trims v and rejects empty values, over-long values, and control
// characters (newlines are allowed only when multiline).
func cleanText(v string, maxRunes int, multiline bool) (string, string) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", "must not be empty; send null to clear"
	}
	if !utf8.ValidString(v) {
		return "", "must be valid UTF-8"
	}
	if utf8.RuneCountInString(v) > maxRunes {
		return "", "must be at most " + strconv.Itoa(maxRunes) + " characters"
	}
	for _, r := range v {
		if unicode.IsControl(r) && !(multiline && r == '\n') {
			return "", "must not contain control characters"
		}
	}
	return v, ""
}

func validateAvatar(a Avatar) string {
	u, err := url.Parse(a.URL)
	switch {
	case a.URL == "" || len(a.URL) > maxAvatarURL || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil:
		return "url must be an https URL of at most 2048 characters"
	case !avatarMimes[a.MimeType]:
		return "mime_type must be one of: image/jpeg, image/png, image/webp, image/heic"
	case a.Width < 1 || a.Width > maxAvatarPixels || a.Height < 1 || a.Height > maxAvatarPixels:
		return "width and height must be between 1 and 10000"
	case a.SizeBytes < 1 || a.SizeBytes > maxAvatarBytes:
		return "size_bytes must be between 1 and 10485760"
	}
	return ""
}

func validateDateOfBirth(v string, now time.Time) (string, string) {
	dob, err := time.Parse(dateOfBirthStyle, v)
	if err != nil {
		return "", "must be a date in YYYY-MM-DD format"
	}
	age := ageOn(dob, now)
	if age < MinimumAge {
		return "", "you must be at least " + strconv.Itoa(MinimumAge) + " years old"
	}
	if age > maximumAge {
		return "", "is not a plausible date of birth"
	}
	return dob.Format(dateOfBirthStyle), ""
}

func ageOn(dob, now time.Time) int {
	now = now.UTC()
	age := now.Year() - dob.Year()
	if now.Month() < dob.Month() || (now.Month() == dob.Month() && now.Day() < dob.Day()) {
		age--
	}
	return age
}

func mapStoreErr(err error, op string) error {
	if stdErrors.Is(err, ErrNotFound) {
		return apperrors.NotFound("user not found")
	}
	return apperrors.Internal(op, err)
}
