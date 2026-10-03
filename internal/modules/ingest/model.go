// Package ingest turns listings from external sources (scrapers, partner
// feeds, seed files) into the trip catalogue. Modelled on scenes-scraper:
// each source carries the rights we hold for its content, every import is a
// recorded run, and listings that disappear from complete runs go stale and
// expire their trips.
package ingest

import (
	"time"

	"github.com/google/uuid"
)

type RightsBasis string

const (
	BasisFirstParty          RightsBasis = "first_party"
	BasisContract            RightsBasis = "contract"
	BasisAffiliate           RightsBasis = "affiliate"
	BasisAPITerms            RightsBasis = "api_terms"
	BasisOfficialPublication RightsBasis = "official_publication"
	// BasisResearchOnly content may be ingested for evaluation but never published.
	BasisResearchOnly RightsBasis = "research_only"
)

var rightsBases = map[RightsBasis]bool{
	BasisFirstParty: true, BasisContract: true, BasisAffiliate: true,
	BasisAPITerms: true, BasisOfficialPublication: true, BasisResearchOnly: true,
}

type Trigger string

const (
	TriggerScraper Trigger = "scraper"
	TriggerManual  Trigger = "manual"
	TriggerSeed    Trigger = "seed"
)

type Source struct {
	ID                 uuid.UUID   `db:"id"`
	Key                string      `db:"source_key"`
	DisplayName        string      `db:"display_name"`
	BaseURL            string      `db:"base_url"`
	Enabled            bool        `db:"enabled"`
	AutoPublish        bool        `db:"auto_publish"`
	RightsBasis        RightsBasis `db:"rights_basis"`
	ImageAllowed       bool        `db:"image_allowed"`
	DescriptionAllowed bool        `db:"description_allowed"`
	AttributionText    *string     `db:"attribution_text"`
	EvidenceRef        string      `db:"evidence_ref"`
	RightsExpiresAt    *time.Time  `db:"rights_expires_at"`
	MissingGraceRuns   int         `db:"missing_grace_runs"`
	CreatedAt          time.Time   `db:"created_at"`
	UpdatedAt          time.Time   `db:"updated_at"`
}

// rights is the effective permission set at a moment; expired rights behave
// like research_only.
type rights struct {
	publishable bool
	images      bool
	description bool
}

func (s Source) rightsAt(now time.Time) rights {
	if s.RightsBasis == BasisResearchOnly || (s.RightsExpiresAt != nil && !now.Before(*s.RightsExpiresAt)) {
		return rights{}
	}
	return rights{publishable: true, images: s.ImageAllowed, description: s.DescriptionAllowed}
}

type SourceInput struct {
	DisplayName        string
	BaseURL            string
	Enabled            bool
	AutoPublish        bool
	RightsBasis        RightsBasis
	ImageAllowed       bool
	DescriptionAllowed bool
	AttributionText    *string
	EvidenceRef        string
	RightsExpiresAt    *time.Time
	MissingGraceRuns   int
}

// Listing is one trip as delivered by a source. It is the import contract
// shared by every producer (Go or Python scrapers, partner feeds, seeds).
type Listing struct {
	ExternalID     string      `json:"external_id"`
	SourceURL      string      `json:"source_url"`
	BookingURL     string      `json:"booking_url"`
	Provider       Provider    `json:"provider"`
	Title          string      `json:"title"`
	Summary        string      `json:"summary,omitempty"`
	Description    string      `json:"description,omitempty"`
	Destination    string      `json:"destination"`
	Region         string      `json:"region,omitempty"`
	CountryCode    string      `json:"country_code,omitempty"`
	Latitude       *float64    `json:"latitude,omitempty"`
	Longitude      *float64    `json:"longitude,omitempty"`
	DurationDays   int         `json:"duration_days"`
	DurationNights int         `json:"duration_nights"`
	Difficulty     string      `json:"difficulty,omitempty"`
	MaxGroupSize   *int        `json:"max_group_size,omitempty"`
	MinAge         *int        `json:"min_age,omitempty"`
	Rating         *float64    `json:"rating,omitempty"`
	ReviewCount    *int        `json:"review_count,omitempty"`
	Interests      []string    `json:"interests,omitempty"`
	Images         []Image     `json:"images,omitempty"`
	Itinerary      []Day       `json:"itinerary,omitempty"`
	Inclusions     []string    `json:"inclusions,omitempty"`
	Exclusions     []string    `json:"exclusions,omitempty"`
	Departures     []Departure `json:"departures,omitempty"`
}

type Provider struct {
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	WebsiteURL  string   `json:"website_url,omitempty"`
	Rating      *float64 `json:"rating,omitempty"`
	ReviewCount *int     `json:"review_count,omitempty"`
}

type Image struct {
	URL    string `json:"url"`
	Width  *int   `json:"width,omitempty"`
	Height *int   `json:"height,omitempty"`
}

type Day struct {
	Day         int    `json:"day"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

type Departure struct {
	ExternalID       string   `json:"external_id,omitempty"`
	StartDate        string   `json:"start_date"`
	EndDate          string   `json:"end_date"`
	DepartureCity    string   `json:"departure_city,omitempty"`
	PriceINR         *float64 `json:"price_inr,omitempty"`
	OriginalPriceINR *float64 `json:"original_price_inr,omitempty"`
	Availability     string   `json:"availability,omitempty"`
	SeatsLeft        *int     `json:"seats_left,omitempty"`
}

// Batch is one import request.
type Batch struct {
	Trigger Trigger
	// Complete means the batch is the source's full current inventory, so
	// listings absent from it count as missing.
	Complete bool
	Listings []Listing
}

type Rejection struct {
	Index      int               `json:"index"`
	ExternalID string            `json:"external_id,omitempty"`
	Errors     map[string]string `json:"errors"`
}

type Run struct {
	ID             uuid.UUID   `db:"id" json:"id"`
	SourceID       uuid.UUID   `db:"source_id" json:"-"`
	Trigger        Trigger     `db:"trigger" json:"trigger"`
	Complete       bool        `db:"complete" json:"complete"`
	Status         string      `db:"status" json:"status"`
	StartedAt      time.Time   `db:"started_at" json:"started_at"`
	FinishedAt     *time.Time  `db:"finished_at" json:"finished_at"`
	ReceivedCount  int         `db:"received_count" json:"received"`
	InsertedCount  int         `db:"inserted_count" json:"inserted"`
	UpdatedCount   int         `db:"updated_count" json:"updated"`
	UnchangedCount int         `db:"unchanged_count" json:"unchanged"`
	RejectedCount  int         `db:"rejected_count" json:"rejected"`
	ExpiredCount   int         `db:"expired_count" json:"expired"`
	RejectionsJSON []byte      `db:"rejections" json:"-"`
	Rejections     []Rejection `db:"-" json:"rejections"`
}

const (
	RunRunning   = "running"
	RunSucceeded = "succeeded"
	RunPartial   = "partial"
	RunFailed    = "failed"
)
