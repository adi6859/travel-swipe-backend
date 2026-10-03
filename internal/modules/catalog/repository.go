package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	stdErrors "errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/adi6859/travel-swipe-backend/internal/platform/database"
)

type Repository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

// query accumulates SQL fragments and positional arguments.
type query struct {
	args []any
}

func (q *query) arg(v any) string {
	q.args = append(q.args, v)
	return "$" + strconv.Itoa(len(q.args))
}

const dateLayout = "2006-01-02"

// istDate is the Indian calendar date at now; departures on it still count as
// upcoming.
func istDate(now time.Time) string {
	return now.In(IST).Format(dateLayout)
}

// rightsGate hides trips whose source rights have lapsed, even before an
// import or admin action updates their status.
func rightsGate(nowArg string) string {
	return `EXISTS (SELECT 1 FROM ingest_sources rs WHERE rs.id = t.source_id
		AND rs.rights_basis <> 'research_only'
		AND (rs.rights_expires_at IS NULL OR rs.rights_expires_at > ` + nowArg + `::timestamptz))`
}

// List returns up to limit trips after pos, ordered by the filter's sort.
// tsQuery is the prefix tsquery built from f.Query (empty when there is no
// text search).
func (r *Repository) List(ctx context.Context, f Filter, tsQuery string, now time.Time, pos *position, limit int) ([]TripCard, error) {
	q := &query{}
	todayArg := q.arg(istDate(now))

	var sortExpr, dir string
	switch f.Sort {
	case SortRelevance:
		tsq, raw := q.arg(tsQuery), q.arg(f.Query)
		sortExpr = `(ts_rank(t.search_vector, to_tsquery('simple', ` + tsq + `))::float8
			+ greatest(similarity(t.title, ` + raw + `), similarity(t.destination, ` + raw + `))::float8)`
		dir = "DESC"
	case SortPriceAsc:
		sortExpr, dir = `COALESCE(u.min_price, 1000000000000)::float8`, "ASC"
	case SortSoonest:
		sortExpr, dir = `COALESCE(u.next_date - DATE '2000-01-01', 1000000)::float8`, "ASC"
	default:
		sortExpr, dir = `COALESCE(t.review_count, -1)::float8`, "DESC"
	}

	where := []string{`t.status = 'published'`, rightsGate(q.arg(now))}
	if f.Query != "" {
		tsq, raw := q.arg(tsQuery), q.arg(f.Query)
		where = append(where, `(t.search_vector @@ to_tsquery('simple', `+tsq+`)
			OR t.title % `+raw+` OR t.destination % `+raw+`)`)
	}
	if f.Category != "" {
		for _, c := range categoryDefs {
			if c.key == f.Category {
				where = append(where, c.condition)
			}
		}
	}
	if f.Interest != "" {
		where = append(where, `EXISTS (SELECT 1 FROM trip_interests fi WHERE fi.trip_id = t.id AND fi.interest_slug = `+q.arg(f.Interest)+`)`)
	}
	if f.Difficulty != "" {
		where = append(where, `t.difficulty = `+q.arg(f.Difficulty))
	}
	if f.MinDays != nil {
		where = append(where, `t.duration_days >= `+q.arg(*f.MinDays))
	}
	if f.MaxDays != nil {
		where = append(where, `t.duration_days <= `+q.arg(*f.MaxDays))
	}

	// Month, departure city and price must hold for the same departure.
	var dep []string
	if f.Month != nil {
		next := f.Month.AddDate(0, 1, 0)
		dep = append(dep, `d.start_date >= `+q.arg(f.Month.Format(dateLayout))+`::date`,
			`d.start_date < `+q.arg(next.Format(dateLayout))+`::date`)
	}
	if f.DepartureCity != "" {
		dep = append(dep, `lower(d.departure_city) = lower(`+q.arg(f.DepartureCity)+`)`)
	}
	if f.MaxPricePaise != nil {
		dep = append(dep, `d.price_paise <= `+q.arg(*f.MaxPricePaise))
	}
	if len(dep) > 0 {
		where = append(where, `EXISTS (SELECT 1 FROM trip_departures d
			WHERE d.trip_id = t.id AND d.removed_at IS NULL AND d.availability <> 'sold_out'
			  AND d.start_date >= `+todayArg+`::date AND `+strings.Join(dep, " AND ")+`)`)
	}

	keyset := "TRUE"
	if pos != nil {
		k, id := q.arg(pos.Key), q.arg(pos.ID)
		cmp := "<"
		if dir == "ASC" {
			cmp = ">"
		}
		keyset = `(s.sort_key ` + cmp + ` ` + k + `::float8 OR (s.sort_key = ` + k + `::float8 AND s.id > ` + id + `::uuid))`
	}

	sqlText := `
		SELECT * FROM (
		    SELECT t.id, t.slug, t.title, t.destination, t.region, t.country_code, t.duration_days,
		           t.duration_nights, t.difficulty, t.rating::float8 AS rating, t.review_count, t.max_group_size,
		           p.name AS provider_name, p.slug AS provider_slug, u.next_date, u.min_price,
		           (SELECT m.url FROM trip_media m WHERE m.trip_id = t.id AND m.display_allowed
		            ORDER BY m.position LIMIT 1) AS cover_url,
		           COALESCE((SELECT json_agg(ti.interest_slug ORDER BY ti.interest_slug)
		                     FROM trip_interests ti WHERE ti.trip_id = t.id), '[]'::json) AS interests,
		           ` + sortExpr + ` AS sort_key
		    FROM trips t
		    JOIN providers p ON p.id = t.provider_id
		    LEFT JOIN LATERAL (
		        SELECT min(d.start_date) AS next_date, min(d.price_paise) AS min_price
		        FROM trip_departures d
		        WHERE d.trip_id = t.id AND d.removed_at IS NULL AND d.availability <> 'sold_out'
		          AND d.start_date >= ` + todayArg + `::date
		    ) u ON TRUE
		    WHERE ` + strings.Join(where, " AND ") + `
		) s
		WHERE ` + keyset + `
		ORDER BY s.sort_key ` + dir + `, s.id
		LIMIT ` + q.arg(limit)

	cards := []TripCard{}
	if err := database.Conn(ctx, r.db).SelectContext(ctx, &cards, sqlText, q.args...); err != nil {
		return nil, err
	}
	for i := range cards {
		cards[i].Interests = []string{}
		if err := json.Unmarshal(cards[i].InterestsJSON, &cards[i].Interests); err != nil {
			return nil, err
		}
	}
	return cards, nil
}

func (r *Repository) Categories(ctx context.Context, now time.Time) ([]Category, error) {
	cols := make([]string, len(categoryDefs))
	for i, c := range categoryDefs {
		cols[i] = `count(*) FILTER (WHERE ` + c.condition + `)`
	}
	row := database.Conn(ctx, r.db).QueryRowxContext(ctx,
		`SELECT `+strings.Join(cols, ", ")+` FROM trips t WHERE t.status = 'published' AND `+rightsGate("$1"), now)
	counts := make([]int, len(categoryDefs))
	dest := make([]any, len(counts))
	for i := range counts {
		dest[i] = &counts[i]
	}
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	out := make([]Category, len(categoryDefs))
	for i, c := range categoryDefs {
		out[i] = Category{Key: c.key, Label: c.label, Count: counts[i]}
	}
	return out, nil
}

// Detail returns a published or expired trip with only displayable media and
// its upcoming departures.
func (r *Repository) Detail(ctx context.Context, id uuid.UUID, now time.Time) (TripDetail, error) {
	conn := database.Conn(ctx, r.db)
	var t TripDetail
	err := conn.GetContext(ctx, &t, `
		SELECT id, slug, title, summary, description, destination, region, country_code,
		       latitude::float8 AS latitude, longitude::float8 AS longitude, duration_days, duration_nights,
		       difficulty, max_group_size, min_age, rating::float8 AS rating, review_count, attribution_text,
		       status, provider_id
		FROM trips t WHERE id = $1 AND status IN ('published', 'expired') AND `+rightsGate("$2"), id, now)
	if stdErrors.Is(err, sql.ErrNoRows) {
		return TripDetail{}, ErrTripNotFound
	}
	if err != nil {
		return TripDetail{}, err
	}

	if err := conn.GetContext(ctx, &t.Provider,
		`SELECT name, slug, website_url, rating::float8 AS rating, review_count FROM providers WHERE id = $1`, t.ProviderID); err != nil {
		return TripDetail{}, err
	}
	t.Interests = []string{}
	if err := conn.SelectContext(ctx, &t.Interests,
		`SELECT interest_slug FROM trip_interests WHERE trip_id = $1 ORDER BY interest_slug`, id); err != nil {
		return TripDetail{}, err
	}
	t.Media = []Media{}
	if err := conn.SelectContext(ctx, &t.Media,
		`SELECT url, width, height FROM trip_media WHERE trip_id = $1 AND display_allowed ORDER BY position`, id); err != nil {
		return TripDetail{}, err
	}
	t.Itinerary = []ItineraryDay{}
	if err := conn.SelectContext(ctx, &t.Itinerary,
		`SELECT day_number, title, description FROM trip_itinerary_days WHERE trip_id = $1 ORDER BY day_number`, id); err != nil {
		return TripDetail{}, err
	}
	var inclusions []struct {
		Kind string `db:"kind"`
		Text string `db:"text"`
	}
	if err := conn.SelectContext(ctx, &inclusions,
		`SELECT kind, text FROM trip_inclusions WHERE trip_id = $1 ORDER BY kind, position`, id); err != nil {
		return TripDetail{}, err
	}
	t.Included, t.Excluded = []string{}, []string{}
	for _, inc := range inclusions {
		if inc.Kind == "included" {
			t.Included = append(t.Included, inc.Text)
		} else {
			t.Excluded = append(t.Excluded, inc.Text)
		}
	}
	t.Departures = []Departure{}
	if err := conn.SelectContext(ctx, &t.Departures, `
		SELECT id, start_date, end_date, departure_city, price_paise, original_price_paise, availability, seats_left
		FROM trip_departures
		WHERE trip_id = $1 AND removed_at IS NULL AND start_date >= $2::date
		ORDER BY start_date, departure_city NULLS FIRST, id`, id, istDate(now)); err != nil {
		return TripDetail{}, err
	}
	return t, nil
}

type outboundTarget struct {
	BookingURL string `db:"booking_url"`
	Status     string `db:"status"`
}

// RecordOutbound logs a booking click and returns the provider URL. The
// departure, when given, must be a current departure of the trip.
func (r *Repository) RecordOutbound(ctx context.Context, tripID uuid.UUID, departureID *uuid.UUID, userID *uuid.UUID, at time.Time) (string, string, error) {
	conn := database.Conn(ctx, r.db)
	var target outboundTarget
	err := conn.GetContext(ctx, &target,
		`SELECT booking_url, status FROM trips t WHERE id = $1 AND status IN ('published', 'expired') AND `+rightsGate("$2"),
		tripID, at)
	if stdErrors.Is(err, sql.ErrNoRows) {
		return "", "", ErrTripNotFound
	}
	if err != nil {
		return "", "", err
	}
	if target.Status != "published" {
		return "", target.Status, nil
	}
	if departureID != nil {
		var ok bool
		if err := conn.GetContext(ctx, &ok, `
			SELECT EXISTS (SELECT 1 FROM trip_departures WHERE id = $1 AND trip_id = $2 AND removed_at IS NULL)`,
			*departureID, tripID); err != nil {
			return "", "", err
		}
		if !ok {
			return "", "", ErrDepartureNotFound
		}
	}
	_, err = conn.ExecContext(ctx, `
		INSERT INTO trip_outbound_clicks (id, trip_id, departure_id, user_id, created_at) VALUES ($1, $2, $3, $4, $5)`,
		uuid.Must(uuid.NewV7()), tripID, departureID, userID, at)
	return target.BookingURL, target.Status, err
}

func (r *Repository) AdminList(ctx context.Context, status, sourceKey string, limit int) ([]AdminTrip, error) {
	q := &query{}
	where := []string{"TRUE"}
	if status != "" {
		where = append(where, `t.status = `+q.arg(status))
	}
	if sourceKey != "" {
		where = append(where, `s.source_key = `+q.arg(sourceKey))
	}
	out := []AdminTrip{}
	err := database.Conn(ctx, r.db).SelectContext(ctx, &out, `
		SELECT t.id, t.title, t.destination, t.status, t.status_reason, s.source_key, p.name AS provider_name,
		       t.published_at, t.updated_at
		FROM trips t
		JOIN ingest_sources s ON s.id = t.source_id
		JOIN providers p ON p.id = t.provider_id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY t.updated_at DESC, t.id
		LIMIT `+q.arg(limit), q.args...)
	return out, err
}

// Publish makes a trip visible if its source's rights allow publishing.
func (r *Repository) Publish(ctx context.Context, id uuid.UUID, at time.Time) error {
	conn := database.Conn(ctx, r.db)
	var publishable *bool
	err := conn.GetContext(ctx, &publishable, `
		SELECT s.rights_basis <> 'research_only' AND (s.rights_expires_at IS NULL OR s.rights_expires_at > $2)
		FROM trips t JOIN ingest_sources s ON s.id = t.source_id
		WHERE t.id = $1`, id, at)
	if stdErrors.Is(err, sql.ErrNoRows) {
		return ErrTripNotFound
	}
	if err != nil {
		return err
	}
	if publishable == nil || !*publishable {
		return ErrNotPublishable
	}
	_, err = conn.ExecContext(ctx, `
		UPDATE trips SET status = 'published', status_reason = NULL, published_at = COALESCE(published_at, $2), updated_at = $2
		WHERE id = $1`, id, at)
	return err
}

func (r *Repository) Hide(ctx context.Context, id uuid.UUID, reason string, at time.Time) error {
	res, err := database.Conn(ctx, r.db).ExecContext(ctx,
		`UPDATE trips SET status = 'hidden', status_reason = $2, updated_at = $3 WHERE id = $1`, id, reason, at)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrTripNotFound
	}
	return nil
}
