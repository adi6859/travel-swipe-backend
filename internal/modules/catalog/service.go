package catalog

import (
	"context"
	stdErrors "errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/adi6859/travel-swipe-backend/pkg/clock"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
	"github.com/adi6859/travel-swipe-backend/pkg/require"
)

const (
	defaultLimit   = 20
	maxLimit       = 50
	maxQueryRunes  = 100
	maxQueryTokens = 8
)

var (
	searchToken  = regexp.MustCompile(`[\p{L}\p{N}]+`)
	interestSlug = regexp.MustCompile(`^[a-z][a-z0-9_]{1,39}$`)
	difficulties = map[string]bool{"easy": true, "moderate": true, "difficult": true, "challenging": true}
	tripStatuses = map[string]bool{"draft": true, "published": true, "hidden": true, "expired": true}
)

type Service struct {
	repo  *Repository
	clock clock.Clock
}

func NewService(repo *Repository, clk clock.Clock) *Service {
	require.AllNotNil("catalog service", map[string]any{"repo": repo, "clock": clk})
	return &Service{repo: repo, clock: clk}
}

// ListTrips validates f and returns one page of published trips.
func (s *Service) ListTrips(ctx context.Context, f Filter) (Page, error) {
	tsQuery, err := normalizeFilter(&f)
	if err != nil {
		return Page{}, err
	}
	pos, err := decodeCursor(f)
	if err != nil {
		return Page{}, apperrors.InvalidFields("request validation failed",
			map[string]string{"cursor": "is invalid or does not match the current filters"})
	}

	cards, err := s.repo.List(ctx, f, tsQuery, s.clock.Now(), pos, f.Limit+1)
	if err != nil {
		return Page{}, apperrors.Internal("list trips", err)
	}
	page := Page{Trips: cards}
	if len(cards) > f.Limit {
		page.Trips = cards[:f.Limit]
		page.NextCursor = encodeCursor(f, page.Trips[f.Limit-1])
	}
	return page, nil
}

func (s *Service) Categories(ctx context.Context) ([]Category, error) {
	out, err := s.repo.Categories(ctx, s.clock.Now())
	if err != nil {
		return nil, apperrors.Internal("count categories", err)
	}
	return out, nil
}

func (s *Service) Trip(ctx context.Context, id uuid.UUID) (TripDetail, error) {
	t, err := s.repo.Detail(ctx, id, s.clock.Now())
	if stdErrors.Is(err, ErrTripNotFound) {
		return TripDetail{}, apperrors.NotFound("trip not found")
	}
	if err != nil {
		return TripDetail{}, apperrors.Internal("load trip", err)
	}
	return t, nil
}

// Outbound records a booking click and returns where to send the user.
func (s *Service) Outbound(ctx context.Context, tripID uuid.UUID, departureID *uuid.UUID, userID *uuid.UUID) (string, error) {
	url, status, err := s.repo.RecordOutbound(ctx, tripID, departureID, userID, s.clock.Now())
	switch {
	case stdErrors.Is(err, ErrTripNotFound):
		return "", apperrors.NotFound("trip not found")
	case stdErrors.Is(err, ErrDepartureNotFound):
		return "", apperrors.InvalidFields("request validation failed", map[string]string{"departure_id": "is not a current departure of this trip"})
	case err != nil:
		return "", apperrors.Internal("record outbound click", err)
	case status != "published":
		return "", apperrors.Conflict("trip is no longer available")
	}
	return url, nil
}

func (s *Service) AdminList(ctx context.Context, status, sourceKey string, limit int) ([]AdminTrip, error) {
	if status != "" && !tripStatuses[status] {
		return nil, apperrors.InvalidFields("request validation failed", map[string]string{"status": "must be draft, published, hidden or expired"})
	}
	out, err := s.repo.AdminList(ctx, status, sourceKey, limit)
	if err != nil {
		return nil, apperrors.Internal("list trips", err)
	}
	return out, nil
}

func (s *Service) Publish(ctx context.Context, id uuid.UUID) error {
	switch err := s.repo.Publish(ctx, id, s.clock.Now()); {
	case stdErrors.Is(err, ErrTripNotFound):
		return apperrors.NotFound("trip not found")
	case stdErrors.Is(err, ErrNotPublishable):
		return apperrors.Conflict("the trip's source rights do not allow publishing")
	case err != nil:
		return apperrors.Internal("publish trip", err)
	}
	return nil
}

func (s *Service) Hide(ctx context.Context, id uuid.UUID, reason string) error {
	reason = strings.TrimSpace(reason)
	if n := utf8.RuneCountInString(reason); n == 0 || n > 200 {
		return apperrors.InvalidFields("request validation failed", map[string]string{"reason": "is required and must be at most 200 characters"})
	}
	switch err := s.repo.Hide(ctx, id, reason, s.clock.Now()); {
	case stdErrors.Is(err, ErrTripNotFound):
		return apperrors.NotFound("trip not found")
	case err != nil:
		return apperrors.Internal("hide trip", err)
	}
	return nil
}

// normalizeFilter validates f in place, applies defaults and returns the
// prefix tsquery for the text search.
func normalizeFilter(f *Filter) (string, error) {
	fields := map[string]string{}

	f.Query = strings.TrimSpace(f.Query)
	var tsQuery string
	if f.Query != "" {
		tokens := searchToken.FindAllString(strings.ToLower(f.Query), maxQueryTokens)
		switch {
		case utf8.RuneCountInString(f.Query) > maxQueryRunes:
			fields["q"] = "must be at most 100 characters"
		case len(tokens) == 0:
			fields["q"] = "must contain letters or digits"
		default:
			for i, tok := range tokens {
				tokens[i] = tok + ":*"
			}
			tsQuery = strings.Join(tokens, " & ")
		}
	}

	switch f.Sort {
	case "":
		f.Sort = SortPopular
		if f.Query != "" {
			f.Sort = SortRelevance
		}
	case SortRelevance:
		if f.Query == "" {
			fields["sort"] = "relevance requires q"
		}
	case SortPopular, SortPriceAsc, SortSoonest:
	default:
		fields["sort"] = "must be one of: relevance, popular, price_asc, soonest"
	}

	if f.Category != "" {
		known := false
		for _, c := range categoryDefs {
			known = known || c.key == f.Category
		}
		if !known {
			fields["category"] = "must be one of: treks, mountains, beaches, road_trips, weekend, international"
		}
	}
	if f.Interest != "" && !interestSlug.MatchString(f.Interest) {
		fields["interest"] = "is not a valid interest slug"
	}
	if f.Difficulty != "" && !difficulties[f.Difficulty] {
		fields["difficulty"] = "must be one of: easy, moderate, difficult, challenging"
	}
	f.DepartureCity = strings.TrimSpace(f.DepartureCity)
	if utf8.RuneCountInString(f.DepartureCity) > 100 {
		fields["departure_city"] = "must be at most 100 characters"
	}
	if f.MinDays != nil && (*f.MinDays < 1 || *f.MinDays > 60) {
		fields["min_days"] = "must be between 1 and 60"
	}
	if f.MaxDays != nil && (*f.MaxDays < 1 || *f.MaxDays > 60) {
		fields["max_days"] = "must be between 1 and 60"
	}
	if f.MinDays != nil && f.MaxDays != nil && *f.MinDays > *f.MaxDays {
		fields["min_days"] = "must not exceed max_days"
	}
	if f.MaxPricePaise != nil && *f.MaxPricePaise < 0 {
		fields["max_price_inr"] = "must not be negative"
	}
	switch {
	case f.Limit == 0:
		f.Limit = defaultLimit
	case f.Limit < 1 || f.Limit > maxLimit:
		fields["limit"] = "must be between 1 and 50"
	}

	if len(fields) > 0 {
		return "", apperrors.InvalidFields("request validation failed", fields)
	}
	return tsQuery, nil
}
