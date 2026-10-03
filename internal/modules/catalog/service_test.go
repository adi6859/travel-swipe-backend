package catalog

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"github.com/adi6859/travel-swipe-backend/internal/modules/ingest"
	"github.com/adi6859/travel-swipe-backend/internal/platform/database"
	"github.com/adi6859/travel-swipe-backend/internal/platform/database/dbtest"
	"github.com/adi6859/travel-swipe-backend/pkg/clock"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
)

const fixtureSource = "fixtures"

func fp(v float64) *float64 { return &v }
func ip(v int) *int         { return &v }

func dep(id, start, end, city string, price float64, availability string) ingest.Departure {
	return ingest.Departure{ExternalID: id, StartDate: start, EndDate: end, DepartureCity: city, PriceINR: fp(price), Availability: availability}
}

func fixtureListings() []ingest.Listing {
	base := func(id, title, destination string, days int, interests ...string) ingest.Listing {
		return ingest.Listing{
			ExternalID: id, SourceURL: "https://example.com/" + id, BookingURL: "https://example.com/" + id + "/book",
			Provider: ingest.Provider{Slug: "acme", Name: "Acme Treks", WebsiteURL: "https://example.com/acme"},
			Title:    title, Destination: destination, DurationDays: days, DurationNights: days - 1, Interests: interests,
		}
	}
	kk := base("kk", "Kedarkantha Winter Trek", "Kedarkantha", 6, "trekking", "mountains")
	kk.Region, kk.Difficulty, kk.ReviewCount, kk.Rating = "Uttarakhand", "moderate", ip(1840), fp(4.7)
	kk.Summary, kk.Description = "Snow summit", "A classic winter trek."
	kk.Latitude, kk.Longitude = fp(31.02), fp(78.17)
	kk.Images = []ingest.Image{{URL: "https://cdn.example.com/kk-1.jpg", Width: ip(1200)}, {URL: "https://cdn.example.com/kk-2.jpg"}}
	kk.Itinerary = []ingest.Day{{Day: 2, Title: "Base camp"}, {Day: 1, Title: "Drive", Description: "To Sankri"}}
	kk.Inclusions, kk.Exclusions = []string{"Meals", "Tents"}, []string{"Insurance"}
	kk.Departures = []ingest.Departure{
		dep("kk-jan", "2027-01-10", "2027-01-15", "Dehradun", 9999, "available"),
		dep("kk-dec", "2026-12-20", "2026-12-25", "Dehradun", 9499, "filling_fast"),
	}

	triund := base("triund", "Triund Weekend Trek", "Triund", 2, "trekking")
	triund.Difficulty, triund.ReviewCount = "easy", ip(2650)
	triund.Departures = []ingest.Departure{
		dep("tr-past", "2026-09-01", "2026-09-02", "McLeod Ganj", 1000, "available"),
		dep("tr-oct", "2026-10-17", "2026-10-18", "McLeod Ganj", 2499, "available"),
	}

	gokarna := base("gokarna", "Gokarna Beach Trek", "Gokarna", 3, "beaches")
	gokarna.Difficulty, gokarna.ReviewCount = "easy", ip(760)
	gokarna.Departures = []ingest.Departure{
		dep("gk-dec", "2026-12-11", "2026-12-13", "Bengaluru", 5499, "sold_out"),
		dep("gk-jan", "2027-01-15", "2027-01-17", "Bengaluru", 5999, "available"),
	}

	ebc := base("ebc", "Everest Base Camp Trek", "Everest Base Camp", 14, "trekking", "mountains")
	ebc.CountryCode, ebc.Difficulty, ebc.ReviewCount = "NP", "difficult", ip(260)
	ebc.Departures = []ingest.Departure{dep("ebc-apr", "2027-04-05", "2027-04-18", "Kathmandu", 105000, "available")}

	spiti := base("spiti", "Spiti Valley Road Trip", "Spiti Valley", 8, "road_trips")
	ladakh := base("ladakh", "Ladakh Road Trip", "Leh", 8, "road_trips")

	return []ingest.Listing{kk, triund, gokarna, ebc, spiti, ladakh}
}

type fixture struct {
	svc *Service
	db  *sqlx.DB
	clk *clock.MockClock
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	db := dbtest.Open(t)
	clk := &clock.MockClock{Current: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)}
	ing := ingest.NewService(ingest.NewRepository(db), database.NewTxManager(db), clk, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	ctx := context.Background()
	_, err := ing.PutSource(ctx, fixtureSource, ingest.SourceInput{
		DisplayName: "Fixtures", BaseURL: "https://example.com", Enabled: true, AutoPublish: true,
		RightsBasis: ingest.BasisFirstParty, ImageAllowed: true, DescriptionAllowed: true, EvidenceRef: "test",
	})
	require.NoError(t, err)
	run, err := ing.Import(ctx, fixtureSource, ingest.Batch{Trigger: ingest.TriggerSeed, Complete: true, Listings: fixtureListings()})
	require.NoError(t, err)
	require.Zero(t, run.RejectedCount, run.Rejections)
	return fixture{svc: NewService(NewRepository(db), clk), db: db, clk: clk}
}

func (f fixture) id(t *testing.T, externalID string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, f.db.Get(&id, `SELECT trip_id FROM source_listings WHERE external_id = $1`, externalID))
	return id
}

func (f fixture) list(t *testing.T, filter Filter) []string {
	t.Helper()
	page, err := f.svc.ListTrips(context.Background(), filter)
	require.NoError(t, err)
	return titles(page.Trips)
}

func titles(cards []TripCard) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.Destination
	}
	return out
}

func TestListDefaultsToPopularWithUpcomingSummary(t *testing.T) {
	f := newFixture(t)
	page, err := f.svc.ListTrips(context.Background(), Filter{})
	require.NoError(t, err)
	require.Empty(t, page.NextCursor)
	require.Equal(t, []string{"Triund", "Kedarkantha", "Gokarna", "Everest Base Camp"}, titles(page.Trips)[:4])

	byDest := map[string]TripCard{}
	for _, c := range page.Trips {
		byDest[c.Destination] = c
	}
	kk := byDest["Kedarkantha"]
	require.Equal(t, "2026-12-20", kk.NextDeparture.Format(dateLayout))
	require.Equal(t, int64(949900), *kk.MinPricePaise)
	require.Equal(t, "https://cdn.example.com/kk-1.jpg", *kk.CoverURL)
	require.Equal(t, []string{"mountains", "trekking"}, kk.Interests)
	require.Equal(t, "Acme Treks", kk.ProviderName)
	require.InDelta(t, 4.7, *kk.Rating, 0.001)

	triund := byDest["Triund"]
	require.Equal(t, "2026-10-17", triund.NextDeparture.Format(dateLayout), "past departures are ignored")
	require.Equal(t, int64(249900), *triund.MinPricePaise)

	gokarna := byDest["Gokarna"]
	require.Equal(t, "2027-01-15", gokarna.NextDeparture.Format(dateLayout), "sold-out departures are ignored")
	require.Equal(t, int64(599900), *gokarna.MinPricePaise)

	spiti := byDest["Spiti Valley"]
	require.Nil(t, spiti.NextDeparture)
	require.Nil(t, spiti.MinPricePaise)
	require.Nil(t, spiti.CoverURL)
}

func TestListFilters(t *testing.T) {
	f := newFixture(t)
	jan := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	dec := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	price := func(inr int64) *int64 { p := inr * 100; return &p }

	cases := []struct {
		name   string
		filter Filter
		want   []string
	}{
		{"treks", Filter{Category: "treks"}, []string{"Triund", "Kedarkantha", "Everest Base Camp"}},
		{"weekend", Filter{Category: "weekend"}, []string{"Triund", "Gokarna"}},
		{"international", Filter{Category: "international"}, []string{"Everest Base Camp"}},
		{"beaches", Filter{Category: "beaches"}, []string{"Gokarna"}},
		{"interest", Filter{Interest: "mountains"}, []string{"Kedarkantha", "Everest Base Camp"}},
		{"difficulty", Filter{Difficulty: "easy"}, []string{"Triund", "Gokarna"}},
		{"duration", Filter{MinDays: ip(5), MaxDays: ip(10), Sort: SortSoonest}, []string{"Kedarkantha", "Spiti Valley", "Leh"}},
		{"month", Filter{Month: &jan}, []string{"Kedarkantha", "Gokarna"}},
		{"city is case-insensitive", Filter{DepartureCity: "dehradun"}, []string{"Kedarkantha"}},
		{"price ignores sold-out", Filter{MaxPricePaise: price(5500)}, []string{"Triund"}},
		{"month and price match one departure", Filter{Month: &dec, MaxPricePaise: price(9500)}, []string{"Kedarkantha"}},
		{"january under 9500", Filter{Month: &jan, MaxPricePaise: price(9500)}, []string{"Gokarna"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := f.list(t, tc.filter)
			if tc.filter.Sort == SortSoonest {
				require.Equal(t, tc.want[0], got[0])
				require.ElementsMatch(t, tc.want, got)
				return
			}
			require.Equal(t, tc.want, got)
		})
	}
}

func TestSearch(t *testing.T) {
	f := newFixture(t)
	require.Equal(t, []string{"Kedarkantha"}, f.list(t, Filter{Query: "kedar"}), "prefix match")
	require.Equal(t, []string{"Kedarkantha"}, f.list(t, Filter{Query: "Kedarkanta"}), "typo match")
	require.Equal(t, "Everest Base Camp", f.list(t, Filter{Query: "everest base"})[0])
	require.ElementsMatch(t, []string{"Spiti Valley", "Leh"}, f.list(t, Filter{Query: "road trip"}))
	require.Empty(t, f.list(t, Filter{Query: "zzzzqqq"}))
}

func TestSorts(t *testing.T) {
	f := newFixture(t)
	require.Equal(t, []string{"Triund", "Gokarna", "Kedarkantha", "Everest Base Camp"}, f.list(t, Filter{Sort: SortPriceAsc})[:4])
	require.Equal(t, []string{"Triund", "Kedarkantha", "Gokarna", "Everest Base Camp"}, f.list(t, Filter{Sort: SortSoonest})[:4])
}

func TestKeysetPagingMatchesFullList(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, filter := range []Filter{{Sort: SortPopular}, {Sort: SortPriceAsc}, {Sort: SortSoonest}, {Query: "trek"}, {Category: "treks", Sort: SortPriceAsc}} {
		full := f.list(t, filter)
		var paged []string
		cursor := ""
		for pages := 0; ; pages++ {
			require.Less(t, pages, 10)
			pf := filter
			pf.Limit, pf.Cursor = 2, cursor
			page, err := f.svc.ListTrips(ctx, pf)
			require.NoError(t, err)
			paged = append(paged, titles(page.Trips)...)
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		require.Equal(t, full, paged, "filter %+v", filter)
	}
}

func TestCursorIsBoundToFilters(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	page, err := f.svc.ListTrips(ctx, Filter{Limit: 2})
	require.NoError(t, err)
	require.NotEmpty(t, page.NextCursor)

	_, err = f.svc.ListTrips(ctx, Filter{Limit: 2, Cursor: page.NextCursor, Category: "treks"})
	require.True(t, apperrors.IsCode(err, apperrors.CodeInvalid))
	require.Contains(t, apperrors.Details(err), "cursor")

	_, err = f.svc.ListTrips(ctx, Filter{Limit: 5, Cursor: page.NextCursor})
	require.NoError(t, err, "page size may change between pages")

	_, err = f.svc.ListTrips(ctx, Filter{Cursor: "not-a-cursor"})
	require.True(t, apperrors.IsCode(err, apperrors.CodeInvalid))
}

func TestListValidation(t *testing.T) {
	f := newFixture(t)
	for name, filter := range map[string]Filter{
		"sort":           {Sort: "newest"},
		"relevance":      {Sort: SortRelevance},
		"category":       {Category: "cruises"},
		"interest":       {Interest: "Bad Slug"},
		"difficulty":     {Difficulty: "extreme"},
		"days":           {MinDays: ip(5), MaxDays: ip(2)},
		"limit":          {Limit: 51},
		"q punctuation":  {Query: "!!!"},
		"price negative": {MaxPricePaise: func() *int64 { v := int64(-1); return &v }()},
	} {
		_, err := f.svc.ListTrips(context.Background(), filter)
		require.True(t, apperrors.IsCode(err, apperrors.CodeInvalid), name)
	}
}

func TestCategoriesCounts(t *testing.T) {
	f := newFixture(t)
	cats, err := f.svc.Categories(context.Background())
	require.NoError(t, err)
	counts := map[string]int{}
	for _, c := range cats {
		counts[c.Key] = c.Count
	}
	require.Equal(t, map[string]int{"treks": 3, "mountains": 2, "beaches": 1, "road_trips": 2, "weekend": 2, "international": 1}, counts)
	require.Equal(t, "treks", cats[0].Key, "categories keep their display order")
}

func TestTripDetail(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.id(t, "kk")

	d, err := f.svc.Trip(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "Kedarkantha Winter Trek", d.Title)
	require.Equal(t, "A classic winter trek.", *d.Description)
	require.Equal(t, "Acme Treks", d.Provider.Name)
	require.Equal(t, []string{"mountains", "trekking"}, d.Interests)
	require.Len(t, d.Media, 2)
	require.Equal(t, 1200, *d.Media[0].Width)
	require.Equal(t, 1, d.Itinerary[0].Day)
	require.Equal(t, []string{"Meals", "Tents"}, d.Included)
	require.Equal(t, []string{"Insurance"}, d.Excluded)
	require.Len(t, d.Departures, 2)
	require.Equal(t, "2026-12-20", d.Departures[0].StartDate.Format(dateLayout))
	require.InDelta(t, 31.02, *d.Latitude, 0.0001)

	_, err = f.db.Exec(`UPDATE trip_media SET display_allowed = FALSE WHERE url LIKE '%kk-2%'`)
	require.NoError(t, err)
	d, err = f.svc.Trip(ctx, id)
	require.NoError(t, err)
	require.Len(t, d.Media, 1, "media without display rights is never served")

	triund, err := f.svc.Trip(ctx, f.id(t, "triund"))
	require.NoError(t, err)
	require.Len(t, triund.Departures, 1, "past departures are omitted")

	for _, status := range []string{"draft", "hidden"} {
		_, err = f.db.Exec(`UPDATE trips SET status = $2 WHERE id = $1`, id, status)
		require.NoError(t, err)
		_, err = f.svc.Trip(ctx, id)
		require.True(t, apperrors.IsCode(err, apperrors.CodeNotFound), status)
	}
	_, err = f.db.Exec(`UPDATE trips SET status = 'expired' WHERE id = $1`, id)
	require.NoError(t, err)
	d, err = f.svc.Trip(ctx, id)
	require.NoError(t, err, "expired trips stay viewable for saved links")
	require.Equal(t, "expired", d.Status)

	_, err = f.svc.Trip(ctx, uuid.New())
	require.True(t, apperrors.IsCode(err, apperrors.CodeNotFound))
}

func TestOutbound(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	kk := f.id(t, "kk")
	userID := uuid.Must(uuid.NewV7())
	_, err := f.db.Exec(`INSERT INTO users (id, phone_e164, phone_verified_at) VALUES ($1, '+919800000001', now())`, userID)
	require.NoError(t, err)
	var depID, otherDep uuid.UUID
	require.NoError(t, f.db.Get(&depID, `SELECT id FROM trip_departures WHERE trip_id = $1 AND departure_key = 'id:kk-dec'`, kk))
	require.NoError(t, f.db.Get(&otherDep, `SELECT id FROM trip_departures WHERE departure_key = 'id:ebc-apr'`))

	url, err := f.svc.Outbound(ctx, kk, &depID, &userID)
	require.NoError(t, err)
	require.Equal(t, "https://example.com/kk/book", url)
	require.Equal(t, 1, countRows(t, f.db, `SELECT count(*) FROM trip_outbound_clicks WHERE trip_id = $1 AND departure_id = $2 AND user_id = $3`, kk, depID, userID))

	_, err = f.svc.Outbound(ctx, kk, &otherDep, &userID)
	require.True(t, apperrors.IsCode(err, apperrors.CodeInvalid))

	_, err = f.svc.Outbound(ctx, uuid.New(), nil, nil)
	require.True(t, apperrors.IsCode(err, apperrors.CodeNotFound))

	_, err = f.db.Exec(`UPDATE trips SET status = 'expired' WHERE id = $1`, kk)
	require.NoError(t, err)
	_, err = f.svc.Outbound(ctx, kk, nil, nil)
	require.True(t, apperrors.IsCode(err, apperrors.CodeConflict))
	require.Equal(t, 1, countRows(t, f.db, `SELECT count(*) FROM trip_outbound_clicks`))
}

func TestLapsedSourceRightsHideTripsImmediately(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, err := f.db.Exec(`UPDATE ingest_sources SET rights_expires_at = $1`, f.clk.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, f.list(t, Filter{}), 6)

	f.clk.Advance(2 * time.Hour)
	require.Empty(t, f.list(t, Filter{}))
	_, err = f.svc.Trip(ctx, f.id(t, "kk"))
	require.True(t, apperrors.IsCode(err, apperrors.CodeNotFound))
	cats, err := f.svc.Categories(ctx)
	require.NoError(t, err)
	require.Zero(t, cats[0].Count)
}

func TestAdminModeration(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	kk := f.id(t, "kk")

	all, err := f.svc.AdminList(ctx, "", fixtureSource, 50)
	require.NoError(t, err)
	require.Len(t, all, 6)

	require.True(t, apperrors.IsCode(f.svc.Hide(ctx, kk, "  "), apperrors.CodeInvalid))
	require.NoError(t, f.svc.Hide(ctx, kk, "Operator asked us to remove it"))
	require.NotContains(t, f.list(t, Filter{}), "Kedarkantha")
	hidden, err := f.svc.AdminList(ctx, "hidden", "", 50)
	require.NoError(t, err)
	require.Len(t, hidden, 1)
	require.Equal(t, "Operator asked us to remove it", *hidden[0].StatusReason)

	require.NoError(t, f.svc.Publish(ctx, kk))
	require.Contains(t, f.list(t, Filter{}), "Kedarkantha")

	_, err = f.db.Exec(`UPDATE ingest_sources SET rights_basis = 'research_only', auto_publish = FALSE`)
	require.NoError(t, err)
	require.True(t, apperrors.IsCode(f.svc.Publish(ctx, kk), apperrors.CodeConflict))

	require.True(t, apperrors.IsCode(f.svc.Publish(ctx, uuid.New()), apperrors.CodeNotFound))
	require.True(t, apperrors.IsCode(f.svc.Hide(ctx, uuid.New(), "x"), apperrors.CodeNotFound))
	_, err = f.svc.AdminList(ctx, "deleted", "", 50)
	require.True(t, apperrors.IsCode(err, apperrors.CodeInvalid))
}

func countRows(t *testing.T, db *sqlx.DB, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, db.Get(&n, query, args...))
	return n
}
