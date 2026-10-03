// Package catalog serves the published trip catalogue to the app (Explore,
// trip detail, outbound booking clicks) and lets admins moderate trips. The
// catalogue itself is written by the ingest module.
package catalog

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrTripNotFound      = errors.New("catalog: trip not found")
	ErrDepartureNotFound = errors.New("catalog: departure not found")
	ErrNotPublishable    = errors.New("catalog: source rights do not allow publishing")
)

// IST anchors "upcoming": the catalogue is India-first and a departure on
// today's Indian date still counts.
var IST = time.FixedZone("IST", 5*3600+1800)

type Sort string

const (
	SortRelevance Sort = "relevance"
	SortPopular   Sort = "popular"
	SortPriceAsc  Sort = "price_asc"
	SortSoonest   Sort = "soonest"
)

type Category struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// categoryDefs is the fixed Explore category list. Categories are derived from
// trip attributes so they need no separate curation.
var categoryDefs = []struct {
	key, label, condition string
}{
	{"treks", "Treks", `EXISTS (SELECT 1 FROM trip_interests ci WHERE ci.trip_id = t.id AND ci.interest_slug = 'trekking')`},
	{"mountains", "Mountains", `EXISTS (SELECT 1 FROM trip_interests ci WHERE ci.trip_id = t.id AND ci.interest_slug = 'mountains')`},
	{"beaches", "Beaches", `EXISTS (SELECT 1 FROM trip_interests ci WHERE ci.trip_id = t.id AND ci.interest_slug = 'beaches')`},
	{"road_trips", "Road Trips", `EXISTS (SELECT 1 FROM trip_interests ci WHERE ci.trip_id = t.id AND ci.interest_slug = 'road_trips')`},
	{"weekend", "Weekend", `t.duration_days <= 3`},
	{"international", "International", `t.country_code <> 'IN'`},
}

type Filter struct {
	Query         string
	Category      string
	Interest      string
	Difficulty    string
	DepartureCity string
	MinDays       *int
	MaxDays       *int
	MaxPricePaise *int64
	Month         *time.Time
	Sort          Sort
	Limit         int
	Cursor        string
}

type TripCard struct {
	ID             uuid.UUID  `db:"id"`
	Slug           string     `db:"slug"`
	Title          string     `db:"title"`
	Destination    string     `db:"destination"`
	Region         *string    `db:"region"`
	CountryCode    string     `db:"country_code"`
	DurationDays   int        `db:"duration_days"`
	DurationNights int        `db:"duration_nights"`
	Difficulty     *string    `db:"difficulty"`
	Rating         *float64   `db:"rating"`
	ReviewCount    *int       `db:"review_count"`
	MaxGroupSize   *int       `db:"max_group_size"`
	ProviderName   string     `db:"provider_name"`
	ProviderSlug   string     `db:"provider_slug"`
	NextDeparture  *time.Time `db:"next_date"`
	MinPricePaise  *int64     `db:"min_price"`
	CoverURL       *string    `db:"cover_url"`
	InterestsJSON  []byte     `db:"interests"`
	SortKey        float64    `db:"sort_key"`
	Interests      []string   `db:"-"`
}

type Page struct {
	Trips      []TripCard
	NextCursor string
}

type Provider struct {
	Name        string   `db:"name"`
	Slug        string   `db:"slug"`
	WebsiteURL  *string  `db:"website_url"`
	Rating      *float64 `db:"rating"`
	ReviewCount *int     `db:"review_count"`
}

type Media struct {
	URL    string `db:"url"`
	Width  *int   `db:"width"`
	Height *int   `db:"height"`
}

type ItineraryDay struct {
	Day         int     `db:"day_number"`
	Title       string  `db:"title"`
	Description *string `db:"description"`
}

type Departure struct {
	ID                 uuid.UUID `db:"id"`
	StartDate          time.Time `db:"start_date"`
	EndDate            time.Time `db:"end_date"`
	DepartureCity      *string   `db:"departure_city"`
	PricePaise         *int64    `db:"price_paise"`
	OriginalPricePaise *int64    `db:"original_price_paise"`
	Availability       string    `db:"availability"`
	SeatsLeft          *int      `db:"seats_left"`
}

type TripDetail struct {
	ID              uuid.UUID `db:"id"`
	Slug            string    `db:"slug"`
	Title           string    `db:"title"`
	Summary         *string   `db:"summary"`
	Description     *string   `db:"description"`
	Destination     string    `db:"destination"`
	Region          *string   `db:"region"`
	CountryCode     string    `db:"country_code"`
	Latitude        *float64  `db:"latitude"`
	Longitude       *float64  `db:"longitude"`
	DurationDays    int       `db:"duration_days"`
	DurationNights  int       `db:"duration_nights"`
	Difficulty      *string   `db:"difficulty"`
	MaxGroupSize    *int      `db:"max_group_size"`
	MinAge          *int      `db:"min_age"`
	Rating          *float64  `db:"rating"`
	ReviewCount     *int      `db:"review_count"`
	AttributionText *string   `db:"attribution_text"`
	Status          string    `db:"status"`
	ProviderID      uuid.UUID `db:"provider_id"`

	Provider   Provider       `db:"-"`
	Interests  []string       `db:"-"`
	Media      []Media        `db:"-"`
	Itinerary  []ItineraryDay `db:"-"`
	Included   []string       `db:"-"`
	Excluded   []string       `db:"-"`
	Departures []Departure    `db:"-"`
}

type AdminTrip struct {
	ID           uuid.UUID  `db:"id" json:"id"`
	Title        string     `db:"title" json:"title"`
	Destination  string     `db:"destination" json:"destination"`
	Status       string     `db:"status" json:"status"`
	StatusReason *string    `db:"status_reason" json:"status_reason"`
	SourceKey    string     `db:"source_key" json:"source_key"`
	ProviderName string     `db:"provider_name" json:"provider_name"`
	PublishedAt  *time.Time `db:"published_at" json:"published_at"`
	UpdatedAt    time.Time  `db:"updated_at" json:"updated_at"`
}
