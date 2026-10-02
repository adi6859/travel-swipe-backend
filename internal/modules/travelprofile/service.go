package travelprofile

import (
	"context"
	stdErrors "errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/adi6859/travel-swipe-backend/internal/platform/database"
	"github.com/adi6859/travel-swipe-backend/pkg/clock"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
	"github.com/adi6859/travel-swipe-backend/pkg/require"
)

const optionsHint = "; see GET /api/v1/travel-profile/options"

type Store interface {
	ActiveInterests(ctx context.Context) ([]Interest, error)
	Get(ctx context.Context, userID uuid.UUID) (Profile, error)
	Save(ctx context.Context, p Profile, at time.Time) error
}

type Service struct {
	store Store
	tx    database.TxRunner
	clock clock.Clock
}

func NewService(store Store, tx database.TxRunner, clk clock.Clock) *Service {
	require.AllNotNil("travelprofile service", map[string]any{"store": store, "tx": tx, "clock": clk})
	return &Service{store: store, tx: tx, clock: clk}
}

func (s *Service) Options(ctx context.Context) (Options, error) {
	interests, err := s.store.ActiveInterests(ctx)
	if err != nil {
		return Options{}, apperrors.Internal("load interests", err)
	}
	return Options{
		TravelStyles: travelStyleOptions,
		Interests:    interests,
		Languages:    languageOptions,
		BudgetBands:  budgetBandOptions,
		GroupSizes:   groupSizeOptions,
		Paces:        paceOptions,
		Smoking:      habitOptions,
		Drinking:     habitOptions,
		Diets:        dietOptions,
	}, nil
}

func (s *Service) Get(ctx context.Context, userID uuid.UUID) (Profile, error) {
	p, err := s.store.Get(ctx, userID)
	if err != nil {
		return Profile{}, apperrors.Internal("load travel profile", err)
	}
	return p, nil
}

// Replace validates in and overwrites the user's travel profile.
func (s *Service) Replace(ctx context.Context, userID uuid.UUID, in Input) (Profile, error) {
	interests, err := s.store.ActiveInterests(ctx)
	if err != nil {
		return Profile{}, apperrors.Internal("load interests", err)
	}
	p, err := validate(userID, in, interests)
	if err != nil {
		return Profile{}, err
	}

	var saved Profile
	err = s.tx.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.store.Save(ctx, p, s.clock.Now()); err != nil {
			return err
		}
		saved, err = s.store.Get(ctx, userID)
		return err
	})
	switch {
	case err == nil:
		return saved, nil
	case stdErrors.Is(err, ErrUserNotFound):
		return Profile{}, apperrors.NotFound("user not found")
	case stdErrors.Is(err, ErrUnknownInterest):
		return Profile{}, apperrors.InvalidFields("request validation failed",
			map[string]string{"interests": "contains an unknown interest" + optionsHint})
	default:
		return Profile{}, apperrors.Internal("save travel profile", err)
	}
}

func validate(userID uuid.UUID, in Input, active []Interest) (Profile, error) {
	interestSet := make(map[string]bool, len(active))
	for _, i := range active {
		interestSet[i.Slug] = true
	}

	fields := map[string]string{}
	p := Profile{UserID: userID}

	var msg string
	if p.TravelStyles, msg = cleanList(in.TravelStyles, travelStyles, maxTravelStyles); msg != "" {
		fields["travel_styles"] = msg
	}
	if p.Interests, msg = cleanList(in.Interests, interestSet, maxInterests); msg != "" {
		fields["interests"] = msg
	}
	if p.Languages, msg = cleanList(in.Languages, languages, maxLanguages); msg != "" {
		fields["languages"] = msg
	}

	for _, f := range []struct {
		name    string
		value   *string
		allowed map[string]bool
		dst     **string
	}{
		{"budget_band", in.BudgetBand, budgetBands, &p.BudgetBand},
		{"group_size", in.GroupSize, groupSizes, &p.GroupSize},
		{"pace", in.Pace, paces, &p.Pace},
		{"smoking", in.Smoking, habits, &p.Smoking},
		{"drinking", in.Drinking, habits, &p.Drinking},
		{"diet", in.Diet, diets, &p.Diet},
	} {
		if f.value == nil {
			continue
		}
		v := normalize(*f.value)
		if !f.allowed[v] {
			fields[f.name] = "is not an allowed value" + optionsHint
			continue
		}
		*f.dst = &v
	}

	if len(fields) > 0 {
		return Profile{}, apperrors.InvalidFields("request validation failed", fields)
	}
	return p, nil
}

// cleanList normalizes, de-duplicates (keeping first occurrence order) and
// checks values against allowed. It always returns a non-nil slice on success.
func cleanList(values []string, allowed map[string]bool, max int) ([]string, string) {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, raw := range values {
		v := normalize(raw)
		if !allowed[v] {
			return nil, "contains a value that is not allowed (" + strconv.Quote(truncate(raw, 40)) + ")" + optionsHint
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	if len(out) > max {
		return nil, "must contain at most " + strconv.Itoa(max) + " values"
	}
	return out, ""
}

func normalize(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
