package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	stdErrors "errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/adi6859/travel-swipe-backend/internal/platform/database"
)

// Deterministic IDs (as in scenes-scraper's catalog projection) make
// re-imports idempotent: the same source listing always maps to the same rows.
var (
	tripNamespace      = uuid.MustParse("5b0f3c1e-8d7a-4e43-9c55-2f1f0a6b7c11")
	providerNamespace  = uuid.MustParse("9e1d2a44-3b6f-4c8e-a1d7-6c0b5e4f3a22")
	departureNamespace = uuid.MustParse("c47a9e10-2f3b-4d5c-8e6f-7a8b9c0d1e33")
	mediaNamespace     = uuid.MustParse("0d8e7f6a-5b4c-4a3d-9e2f-1a0b9c8d7e44")
)

const staleReason = "listing missing from source"

var ErrSourceNotFound = stdErrors.New("ingest: source not found")

type Repository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

const sourceColumns = `id, source_key, display_name, base_url, enabled, auto_publish, rights_basis,
	image_allowed, description_allowed, attribution_text, evidence_ref, rights_expires_at,
	missing_grace_runs, created_at, updated_at`

func (r *Repository) ListSources(ctx context.Context) ([]Source, error) {
	out := []Source{}
	err := database.Conn(ctx, r.db).SelectContext(ctx, &out, `SELECT `+sourceColumns+` FROM ingest_sources ORDER BY source_key`)
	return out, err
}

// LockSource serializes imports and rights changes per source for the rest of
// the transaction, then loads it.
func (r *Repository) LockSource(ctx context.Context, key string) (Source, error) {
	conn := database.Conn(ctx, r.db)
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('ingest:' || $1, 0))`, key); err != nil {
		return Source{}, err
	}
	var s Source
	err := conn.GetContext(ctx, &s, `SELECT `+sourceColumns+` FROM ingest_sources WHERE source_key = $1`, key)
	if stdErrors.Is(err, sql.ErrNoRows) {
		return Source{}, ErrSourceNotFound
	}
	return s, err
}

func (r *Repository) GetSource(ctx context.Context, key string) (Source, error) {
	var s Source
	err := database.Conn(ctx, r.db).GetContext(ctx, &s, `SELECT `+sourceColumns+` FROM ingest_sources WHERE source_key = $1`, key)
	if stdErrors.Is(err, sql.ErrNoRows) {
		return Source{}, ErrSourceNotFound
	}
	return s, err
}

// UpsertSource creates or updates a source. When rights change it brings the
// catalogue in line: media visibility follows image rights, revoked
// description rights erase stored text, and widened rights force the next
// import to rewrite every listing.
func (r *Repository) UpsertSource(ctx context.Context, key string, in SourceInput, prev *Source, now time.Time) (Source, error) {
	conn := database.Conn(ctx, r.db)
	var s Source
	err := conn.GetContext(ctx, &s, `
		INSERT INTO ingest_sources (id, source_key, display_name, base_url, enabled, auto_publish, rights_basis,
		    image_allowed, description_allowed, attribution_text, evidence_ref, rights_expires_at,
		    missing_grace_runs, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14)
		ON CONFLICT (source_key) DO UPDATE SET
		    display_name = EXCLUDED.display_name, base_url = EXCLUDED.base_url, enabled = EXCLUDED.enabled,
		    auto_publish = EXCLUDED.auto_publish, rights_basis = EXCLUDED.rights_basis,
		    image_allowed = EXCLUDED.image_allowed, description_allowed = EXCLUDED.description_allowed,
		    attribution_text = EXCLUDED.attribution_text, evidence_ref = EXCLUDED.evidence_ref,
		    rights_expires_at = EXCLUDED.rights_expires_at, missing_grace_runs = EXCLUDED.missing_grace_runs,
		    updated_at = EXCLUDED.updated_at
		RETURNING `+sourceColumns,
		uuid.Must(uuid.NewV7()), key, in.DisplayName, in.BaseURL, in.Enabled, in.AutoPublish, in.RightsBasis,
		in.ImageAllowed, in.DescriptionAllowed, in.AttributionText, in.EvidenceRef, in.RightsExpiresAt,
		in.MissingGraceRuns, now)
	if err != nil || prev == nil {
		return s, err
	}

	before, after := prev.rightsAt(now), s.rightsAt(now)
	if _, err := conn.ExecContext(ctx, `
		UPDATE trip_media SET display_allowed = $2
		WHERE trip_id IN (SELECT id FROM trips WHERE source_id = $1)`, s.ID, after.images); err != nil {
		return s, err
	}
	if _, err := conn.ExecContext(ctx, `UPDATE trips SET attribution_text = $2 WHERE source_id = $1`, s.ID, s.AttributionText); err != nil {
		return s, err
	}
	if before.description && !after.description {
		if _, err := conn.ExecContext(ctx, `UPDATE trips SET description = NULL, updated_at = $2 WHERE source_id = $1`, s.ID, now); err != nil {
			return s, err
		}
		for _, q := range []string{
			`UPDATE trip_itinerary_days SET description = NULL WHERE trip_id IN (SELECT id FROM trips WHERE source_id = $1)`,
			`DELETE FROM trip_inclusions WHERE trip_id IN (SELECT id FROM trips WHERE source_id = $1)`,
		} {
			if _, err := conn.ExecContext(ctx, q, s.ID); err != nil {
				return s, err
			}
		}
	}
	if !after.publishable {
		if _, err := conn.ExecContext(ctx, `
			UPDATE trips SET status = 'draft', status_reason = 'source rights do not allow publishing', updated_at = $2
			WHERE source_id = $1 AND status IN ('published', 'expired')`, s.ID, now); err != nil {
			return s, err
		}
	}
	if (!before.description && after.description) || (!before.images && after.images) {
		if _, err := conn.ExecContext(ctx, `UPDATE source_listings SET content_hash = '' WHERE source_id = $1`, s.ID); err != nil {
			return s, err
		}
	}
	return s, nil
}

func (r *Repository) ActiveInterests(ctx context.Context) (map[string]bool, error) {
	var slugs []string
	if err := database.Conn(ctx, r.db).SelectContext(ctx, &slugs, `SELECT slug FROM interests WHERE active`); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(slugs))
	for _, s := range slugs {
		out[s] = true
	}
	return out, nil
}

func (r *Repository) CreateRun(ctx context.Context, run Run) error {
	_, err := database.Conn(ctx, r.db).ExecContext(ctx, `
		INSERT INTO ingest_runs (id, source_id, trigger, complete, status, started_at, received_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		run.ID, run.SourceID, run.Trigger, run.Complete, run.Status, run.StartedAt, run.ReceivedCount)
	return err
}

func (r *Repository) FinishRun(ctx context.Context, run Run, errorSummary *string) error {
	rejections, err := json.Marshal(run.Rejections)
	if err != nil {
		return err
	}
	_, err = database.Conn(ctx, r.db).ExecContext(ctx, `
		UPDATE ingest_runs SET status = $2, finished_at = $3, inserted_count = $4, updated_count = $5,
		    unchanged_count = $6, rejected_count = $7, expired_count = $8, rejections = $9::jsonb, error_summary = $10
		WHERE id = $1`,
		run.ID, run.Status, run.FinishedAt, run.InsertedCount, run.UpdatedCount, run.UnchangedCount,
		run.RejectedCount, run.ExpiredCount, string(rejections), errorSummary)
	return err
}

const runColumns = `id, source_id, trigger, complete, status, started_at, finished_at, received_count,
	inserted_count, updated_count, unchanged_count, rejected_count, expired_count, rejections`

func (r *Repository) ListRuns(ctx context.Context, sourceID uuid.UUID, limit int) ([]Run, error) {
	runs := []Run{}
	if err := database.Conn(ctx, r.db).SelectContext(ctx, &runs,
		`SELECT `+runColumns+` FROM ingest_runs WHERE source_id = $1 ORDER BY started_at DESC, id DESC LIMIT $2`,
		sourceID, limit); err != nil {
		return nil, err
	}
	for i := range runs {
		runs[i].Rejections = []Rejection{}
		if err := json.Unmarshal(runs[i].RejectionsJSON, &runs[i].Rejections); err != nil {
			return nil, err
		}
	}
	return runs, nil
}

type existingListing struct {
	ID          uuid.UUID  `db:"id"`
	ContentHash string     `db:"content_hash"`
	TripID      *uuid.UUID `db:"trip_id"`
}

func (r *Repository) findListing(ctx context.Context, sourceID uuid.UUID, externalID string) (*existingListing, error) {
	var l existingListing
	err := database.Conn(ctx, r.db).GetContext(ctx, &l,
		`SELECT id, content_hash, trip_id FROM source_listings WHERE source_id = $1 AND external_id = $2`, sourceID, externalID)
	if stdErrors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &l, err
}

// touchListings records that listings were present in a run without rewriting
// their content, and revives trips that had expired only because they went
// missing (when the source may still publish).
func (r *Repository) touchListings(ctx context.Context, sourceID, runID uuid.UUID, externalIDs []string, publishable bool, now time.Time) error {
	if len(externalIDs) == 0 {
		return nil
	}
	conn := database.Conn(ctx, r.db)
	if _, err := conn.ExecContext(ctx, `
		UPDATE source_listings
		SET last_seen_run_id = $2, last_seen_at = $3, consecutive_missing_runs = 0, stale_at = NULL
		WHERE source_id = $1 AND external_id = ANY($4::text[])`,
		sourceID, runID, now, externalIDs); err != nil {
		return err
	}
	if !publishable {
		return nil
	}
	_, err := conn.ExecContext(ctx, `
		UPDATE trips SET status = 'published', status_reason = NULL, updated_at = $3
		WHERE status = 'expired' AND status_reason = $4 AND published_at IS NOT NULL
		  AND id IN (SELECT trip_id FROM source_listings WHERE source_id = $1 AND external_id = ANY($2::text[]))`,
		sourceID, externalIDs, now, staleReason)
	return err
}

// markMissing ages listings absent from a complete run and expires the trips
// of listings past the grace period. It returns how many trips expired.
func (r *Repository) markMissing(ctx context.Context, src Source, runID uuid.UUID, now time.Time) (int, error) {
	conn := database.Conn(ctx, r.db)
	var staleTrips []uuid.UUID
	err := conn.SelectContext(ctx, &staleTrips, `
		WITH aged AS (
		    UPDATE source_listings
		    SET consecutive_missing_runs = consecutive_missing_runs + 1,
		        stale_at = CASE WHEN consecutive_missing_runs + 1 >= $3 THEN $4::timestamptz ELSE NULL END
		    WHERE source_id = $1 AND last_seen_run_id <> $2 AND stale_at IS NULL
		    RETURNING trip_id, stale_at
		)
		SELECT trip_id FROM aged WHERE stale_at IS NOT NULL AND trip_id IS NOT NULL`,
		src.ID, runID, src.MissingGraceRuns, now)
	if err != nil || len(staleTrips) == 0 {
		return 0, err
	}
	res, err := conn.ExecContext(ctx, `
		UPDATE trips SET status = 'expired', status_reason = $2, updated_at = $3
		WHERE id = ANY($1::uuid[]) AND status = 'published'`, uuidStrings(staleTrips), staleReason, now)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// saveListing writes a changed or new listing and its trip. It reports
// whether the trip was newly created.
func (r *Repository) saveListing(ctx context.Context, src Source, runID uuid.UUID, l normalized, prev *existingListing, now time.Time) (bool, error) {
	conn := database.Conn(ctx, r.db)
	rights := src.rightsAt(now)

	providerID := uuid.NewSHA1(providerNamespace, []byte(l.Provider.Slug))
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO providers (id, slug, name, website_url, rating, review_count, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name,
		    website_url = COALESCE(EXCLUDED.website_url, providers.website_url),
		    rating = COALESCE(EXCLUDED.rating, providers.rating),
		    review_count = COALESCE(EXCLUDED.review_count, providers.review_count),
		    updated_at = EXCLUDED.updated_at`,
		providerID, l.Provider.Slug, l.Provider.Name, nullIfEmpty(l.Provider.WebsiteURL),
		l.Provider.Rating, l.Provider.ReviewCount, now); err != nil {
		return false, err
	}
	if err := conn.GetContext(ctx, &providerID, `SELECT id FROM providers WHERE slug = $1`, l.Provider.Slug); err != nil {
		return false, err
	}

	tripID := uuid.NewSHA1(tripNamespace, []byte(src.Key+"|"+l.ExternalID))
	description := ""
	if rights.description {
		description = l.Description
	}
	status, publishedAt := "draft", (*time.Time)(nil)
	if src.AutoPublish && rights.publishable {
		status, publishedAt = "published", &now
	}

	var inserted bool
	err := conn.GetContext(ctx, &inserted, `
		INSERT INTO trips (id, provider_id, source_id, slug, title, summary, description, destination, region,
		    country_code, latitude, longitude, duration_days, duration_nights, difficulty, max_group_size, min_age,
		    rating, review_count, booking_url, attribution_text, status, published_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $24)
		ON CONFLICT (id) DO UPDATE SET
		    provider_id = EXCLUDED.provider_id, title = EXCLUDED.title, summary = EXCLUDED.summary,
		    description = EXCLUDED.description, destination = EXCLUDED.destination, region = EXCLUDED.region,
		    country_code = EXCLUDED.country_code, latitude = EXCLUDED.latitude, longitude = EXCLUDED.longitude,
		    duration_days = EXCLUDED.duration_days, duration_nights = EXCLUDED.duration_nights,
		    difficulty = EXCLUDED.difficulty, max_group_size = EXCLUDED.max_group_size, min_age = EXCLUDED.min_age,
		    rating = EXCLUDED.rating, review_count = EXCLUDED.review_count, booking_url = EXCLUDED.booking_url,
		    attribution_text = EXCLUDED.attribution_text, updated_at = EXCLUDED.updated_at
		RETURNING (xmax = 0)`,
		tripID, providerID, src.ID, slugFor(l.Title, tripID), l.Title, nullIfEmpty(l.Summary), nullIfEmpty(description),
		l.Destination, nullIfEmpty(l.Region), l.CountryCode, l.Latitude, l.Longitude, l.DurationDays, l.DurationNights,
		nullIfEmpty(l.Difficulty), l.MaxGroupSize, l.MinAge, l.Rating, l.ReviewCount, l.BookingURL,
		src.AttributionText, status, publishedAt, now)
	if err != nil {
		return false, err
	}

	if err := r.replaceTripChildren(ctx, tripID, l, rights); err != nil {
		return false, err
	}
	if err := r.syncDepartures(ctx, tripID, l.Departures, now); err != nil {
		return false, err
	}

	payload, err := json.Marshal(l.Listing)
	if err != nil {
		return false, err
	}
	listingID := uuid.Must(uuid.NewV7())
	if prev != nil {
		listingID = prev.ID
	}
	_, err = conn.ExecContext(ctx, `
		INSERT INTO source_listings (id, source_id, external_id, source_url, content_hash, payload, trip_id,
		    first_seen_run_id, last_seen_run_id, first_seen_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $8, $9, $9)
		ON CONFLICT (source_id, external_id) DO UPDATE SET
		    source_url = EXCLUDED.source_url, content_hash = EXCLUDED.content_hash, payload = EXCLUDED.payload,
		    trip_id = EXCLUDED.trip_id, last_seen_run_id = EXCLUDED.last_seen_run_id,
		    last_seen_at = EXCLUDED.last_seen_at, consecutive_missing_runs = 0, stale_at = NULL`,
		listingID, src.ID, l.ExternalID, l.SourceURL, l.ContentHash, string(payload), tripID, runID, now)
	if err != nil {
		return false, err
	}

	if rights.publishable {
		if _, err := conn.ExecContext(ctx, `
			UPDATE trips SET status = 'published', status_reason = NULL
			WHERE id = $1 AND status = 'expired' AND status_reason = $2 AND published_at IS NOT NULL`,
			tripID, staleReason); err != nil {
			return false, err
		}
	}
	return inserted, nil
}

func (r *Repository) replaceTripChildren(ctx context.Context, tripID uuid.UUID, l normalized, rights rights) error {
	conn := database.Conn(ctx, r.db)
	for _, table := range []string{"trip_interests", "trip_media", "trip_itinerary_days", "trip_inclusions"} {
		if _, err := conn.ExecContext(ctx, `DELETE FROM `+table+` WHERE trip_id = $1`, tripID); err != nil {
			return err
		}
	}
	if len(l.Interests) > 0 {
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO trip_interests (trip_id, interest_slug) SELECT $1, unnest($2::text[])`, tripID, l.Interests); err != nil {
			return err
		}
	}
	for i, img := range l.Images {
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO trip_media (id, trip_id, url, width, height, position, display_allowed)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			uuid.NewSHA1(mediaNamespace, []byte(tripID.String()+"|"+img.URL)), tripID, img.URL, img.Width, img.Height, i, rights.images); err != nil {
			return err
		}
	}
	for _, d := range l.Itinerary {
		desc := ""
		if rights.description {
			desc = d.Description
		}
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO trip_itinerary_days (trip_id, day_number, title, description) VALUES ($1, $2, $3, $4)`,
			tripID, d.Day, d.Title, nullIfEmpty(desc)); err != nil {
			return err
		}
	}
	if !rights.description {
		return nil
	}
	for kind, items := range map[string][]string{"included": l.Inclusions, "excluded": l.Exclusions} {
		for i, text := range items {
			if _, err := conn.ExecContext(ctx,
				`INSERT INTO trip_inclusions (trip_id, kind, position, text) VALUES ($1, $2, $3, $4)`,
				tripID, kind, i, text); err != nil {
				return err
			}
		}
	}
	return nil
}

type departureState struct {
	Key        string `db:"departure_key"`
	PricePaise *int64 `db:"price_paise"`
	Removed    bool   `db:"removed"`
}

// syncDepartures upserts the listing's departures, records price changes, and
// marks departures that the source no longer lists as removed.
func (r *Repository) syncDepartures(ctx context.Context, tripID uuid.UUID, deps []normalizedDeparture, now time.Time) error {
	conn := database.Conn(ctx, r.db)
	var current []departureState
	if err := conn.SelectContext(ctx, &current,
		`SELECT departure_key, price_paise, removed_at IS NOT NULL AS removed FROM trip_departures WHERE trip_id = $1`, tripID); err != nil {
		return err
	}
	existing := make(map[string]departureState, len(current))
	for _, d := range current {
		existing[d.Key] = d
	}

	keys := make([]string, 0, len(deps))
	for _, d := range deps {
		keys = append(keys, d.Key)
		id := uuid.NewSHA1(departureNamespace, []byte(tripID.String()+"|"+d.Key))
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO trip_departures (id, trip_id, departure_key, start_date, end_date, departure_city, price_paise,
			    original_price_paise, availability, seats_left, last_seen_at)
			VALUES ($1, $2, $3, $4::date, $5::date, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (trip_id, departure_key) DO UPDATE SET
			    start_date = EXCLUDED.start_date, end_date = EXCLUDED.end_date, departure_city = EXCLUDED.departure_city,
			    price_paise = EXCLUDED.price_paise, original_price_paise = EXCLUDED.original_price_paise,
			    availability = EXCLUDED.availability, seats_left = EXCLUDED.seats_left,
			    last_seen_at = EXCLUDED.last_seen_at, removed_at = NULL`,
			id, tripID, d.Key, d.StartDate.Format(dateLayout), d.EndDate.Format(dateLayout), d.DepartureCity,
			d.PricePaise, d.OriginalPricePaise, d.Availability, d.SeatsLeft, now); err != nil {
			return err
		}
		prev, seen := existing[d.Key]
		if !seen || prev.Removed || !samePrice(prev.PricePaise, d.PricePaise) {
			if _, err := conn.ExecContext(ctx,
				`INSERT INTO trip_price_history (departure_id, observed_at, price_paise) VALUES ($1, $2, $3)`,
				id, now, d.PricePaise); err != nil {
				return err
			}
		}
	}
	_, err := conn.ExecContext(ctx, `
		UPDATE trip_departures SET removed_at = $3
		WHERE trip_id = $1 AND removed_at IS NULL AND NOT (departure_key = ANY($2::text[]))`, tripID, keys, now)
	return err
}

func samePrice(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

var slugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

// slugFor is stable for a trip: the title part is fixed at first insert (the
// upsert never rewrites slug) and the ID suffix guarantees uniqueness.
func slugFor(title string, id uuid.UUID) string {
	base := strings.Trim(slugUnsafe.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(base) > 60 {
		base = strings.TrimRight(base[:60], "-")
	}
	if base == "" {
		base = "trip"
	}
	return base + "-" + strings.ReplaceAll(id.String(), "-", "")[:8]
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}
