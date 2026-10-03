package ingest

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"github.com/adi6859/travel-swipe-backend/internal/platform/database"
	"github.com/adi6859/travel-swipe-backend/internal/platform/database/dbtest"
	"github.com/adi6859/travel-swipe-backend/pkg/clock"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
)

const testSource = "test_source"

func newTestIngest(t *testing.T) (*Service, *sqlx.DB, *clock.MockClock) {
	t.Helper()
	db := dbtest.Open(t)
	clk := &clock.MockClock{Current: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)}
	svc := NewService(NewRepository(db), database.NewTxManager(db), clk, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	return svc, db, clk
}

func openSource() SourceInput {
	return SourceInput{
		DisplayName: "Test source", BaseURL: "https://example.com", Enabled: true, AutoPublish: true,
		RightsBasis: BasisFirstParty, ImageAllowed: true, DescriptionAllowed: true, EvidenceRef: "test fixture",
		MissingGraceRuns: 2,
	}
}

func putSource(t *testing.T, svc *Service, in SourceInput) Source {
	t.Helper()
	src, err := svc.PutSource(context.Background(), testSource, in)
	require.NoError(t, err)
	return src
}

func testListing(id string) Listing {
	return Listing{
		ExternalID: id, SourceURL: "https://example.com/" + id, BookingURL: "https://example.com/" + id + "/book",
		Provider: Provider{Slug: "acme", Name: "Acme Treks"}, Title: "Trip " + id, Description: "About " + id,
		Destination: "Manali", DurationDays: 3, DurationNights: 2, Interests: []string{"trekking"},
		Images:     []Image{{URL: "https://cdn.example.com/" + id + ".jpg"}},
		Inclusions: []string{"Meals"},
		Itinerary:  []Day{{Day: 1, Title: "Arrive", Description: "Check in"}},
		Departures: []Departure{{ExternalID: "d1", StartDate: "2026-12-01", EndDate: "2026-12-03", PriceINR: fptr(1000)}},
	}
}

func importListings(t *testing.T, svc *Service, complete bool, listings ...Listing) Run {
	t.Helper()
	run, err := svc.Import(context.Background(), testSource, Batch{Trigger: TriggerManual, Complete: complete, Listings: listings})
	require.NoError(t, err)
	return run
}

func tripID(externalID string) uuid.UUID {
	return uuid.NewSHA1(tripNamespace, []byte(testSource+"|"+externalID))
}

type tripRow struct {
	Slug         string  `db:"slug"`
	Title        string  `db:"title"`
	Status       string  `db:"status"`
	StatusReason *string `db:"status_reason"`
	Description  *string `db:"description"`
}

func getTrip(t *testing.T, db *sqlx.DB, externalID string) tripRow {
	t.Helper()
	var row tripRow
	require.NoError(t, db.Get(&row, `SELECT slug, title, status, status_reason, description FROM trips WHERE id = $1`, tripID(externalID)))
	return row
}

func count(t *testing.T, db *sqlx.DB, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, db.Get(&n, query, args...))
	return n
}

func TestImportInsertsUpdatesAndSkipsUnchanged(t *testing.T) {
	svc, db, clk := newTestIngest(t)
	putSource(t, svc, openSource())

	run := importListings(t, svc, true, testListing("a"), testListing("b"))
	require.Equal(t, RunSucceeded, run.Status)
	require.Equal(t, 2, run.InsertedCount)
	a := getTrip(t, db, "a")
	require.Equal(t, "published", a.Status)
	require.Equal(t, "About a", *a.Description)
	require.Regexp(t, `^trip-a-[0-9a-f]{8}$`, a.Slug)

	clk.Advance(time.Hour)
	run = importListings(t, svc, true, testListing("a"), testListing("b"))
	require.Equal(t, 2, run.UnchangedCount)
	require.Zero(t, run.InsertedCount+run.UpdatedCount)

	renamed := testListing("a")
	renamed.Title = "Renamed trip"
	run = importListings(t, svc, true, renamed, testListing("b"))
	require.Equal(t, 1, run.UpdatedCount)
	require.Equal(t, 1, run.UnchangedCount)
	after := getTrip(t, db, "a")
	require.Equal(t, "Renamed trip", after.Title)
	require.Equal(t, a.Slug, after.Slug, "slugs are stable across title changes")

	runs, err := svc.ListRuns(context.Background(), testSource, 10)
	require.NoError(t, err)
	require.Len(t, runs, 3)
	require.Equal(t, 1, runs[0].UpdatedCount, "runs are newest first")
}

func TestImportSyncsDeparturesAndPriceHistory(t *testing.T) {
	svc, db, clk := newTestIngest(t)
	putSource(t, svc, openSource())

	l := testListing("a")
	l.Departures = append(l.Departures, Departure{StartDate: "2026-12-10", EndDate: "2026-12-12", DepartureCity: "Delhi", PriceINR: fptr(2000)})
	importListings(t, svc, true, l)
	require.Equal(t, 2, count(t, db, `SELECT count(*) FROM trip_departures WHERE removed_at IS NULL`))
	require.Equal(t, 2, count(t, db, `SELECT count(*) FROM trip_price_history`))

	clk.Advance(time.Hour)
	changed := testListing("a")
	changed.Departures[0].PriceINR = fptr(1200)
	importListings(t, svc, true, changed)
	var price int64
	require.NoError(t, db.Get(&price, `SELECT price_paise FROM trip_departures WHERE departure_key = 'id:d1'`))
	require.Equal(t, int64(120000), price)
	require.Equal(t, 1, count(t, db, `SELECT count(*) FROM trip_departures WHERE departure_key = 'date:2026-12-10|delhi' AND removed_at IS NOT NULL`),
		"departures the source stops listing are marked removed")
	require.Equal(t, 3, count(t, db, `SELECT count(*) FROM trip_price_history`), "a price change adds history")

	clk.Advance(time.Hour)
	importListings(t, svc, true, l)
	require.Equal(t, 2, count(t, db, `SELECT count(*) FROM trip_departures WHERE removed_at IS NULL`), "a relisted departure comes back")
	require.Equal(t, 5, count(t, db, `SELECT count(*) FROM trip_price_history`))
}

func TestImportExpiresMissingListingsAfterGraceAndRevives(t *testing.T) {
	svc, db, clk := newTestIngest(t)
	putSource(t, svc, openSource())

	importListings(t, svc, true, testListing("a"), testListing("b"))

	clk.Advance(time.Hour)
	run := importListings(t, svc, true, testListing("a"))
	require.Zero(t, run.ExpiredCount, "one miss is within the grace period")
	require.Equal(t, "published", getTrip(t, db, "b").Status)

	clk.Advance(time.Hour)
	run = importListings(t, svc, false, testListing("a"))
	require.Zero(t, run.ExpiredCount, "partial batches never age listings")

	clk.Advance(time.Hour)
	run = importListings(t, svc, true, testListing("a"))
	require.Equal(t, 1, run.ExpiredCount)
	b := getTrip(t, db, "b")
	require.Equal(t, "expired", b.Status)
	require.Equal(t, staleReason, *b.StatusReason)

	clk.Advance(time.Hour)
	run = importListings(t, svc, true, testListing("a"), testListing("b"))
	require.Equal(t, 2, run.UnchangedCount)
	require.Equal(t, "published", getTrip(t, db, "b").Status, "an unchanged listing that reappears is revived")
}

func TestImportRejectsInvalidListingsIndividually(t *testing.T) {
	svc, db, clk := newTestIngest(t)
	in := openSource()
	in.MissingGraceRuns = 1
	putSource(t, svc, in)
	importListings(t, svc, true, testListing("a"), testListing("b"))

	clk.Advance(time.Hour)
	broken := testListing("b")
	broken.BookingURL = "http://insecure.example.com"
	unknown := testListing("c")
	unknown.Interests = []string{"skydiving"}
	run := importListings(t, svc, true, testListing("a"), broken, unknown, testListing("a"))
	require.Equal(t, RunPartial, run.Status)
	require.Equal(t, 3, run.RejectedCount)
	require.Equal(t, 1, run.UnchangedCount)
	require.Zero(t, run.ExpiredCount, "a listing rejected by validation is still counted as seen")
	require.Equal(t, "published", getTrip(t, db, "b").Status)
	require.Equal(t, 0, count(t, db, `SELECT count(*) FROM trips WHERE id = $1`, tripID("c")))

	runs, err := svc.ListRuns(context.Background(), testSource, 1)
	require.NoError(t, err)
	require.Len(t, runs[0].Rejections, 3)
	require.Equal(t, "b", runs[0].Rejections[0].ExternalID)
	require.Contains(t, runs[0].Rejections[0].Errors, "booking_url")
	require.Contains(t, runs[0].Rejections[2].Errors["external_id"], "more than once")
}

func TestResearchOnlySourcesNeverPublish(t *testing.T) {
	svc, db, _ := newTestIngest(t)

	in := openSource()
	in.RightsBasis = BasisResearchOnly
	_, err := svc.PutSource(context.Background(), testSource, in)
	require.True(t, apperrors.IsCode(err, apperrors.CodeInvalid), "research_only with auto_publish is rejected")

	in.AutoPublish = false
	putSource(t, svc, in)
	importListings(t, svc, true, testListing("a"))
	a := getTrip(t, db, "a")
	require.Equal(t, "draft", a.Status)
	require.Nil(t, a.Description, "research_only grants no description rights")
	require.Equal(t, 0, count(t, db, `SELECT count(*) FROM trip_media WHERE display_allowed`))
	require.Equal(t, 0, count(t, db, `SELECT count(*) FROM trip_inclusions`))
}

func TestExpiredRightsBehaveLikeNoRights(t *testing.T) {
	svc, db, clk := newTestIngest(t)
	in := openSource()
	past := clk.Now().Add(-time.Hour)
	in.RightsExpiresAt = &past
	putSource(t, svc, in)

	importListings(t, svc, true, testListing("a"))
	require.Equal(t, "draft", getTrip(t, db, "a").Status)
	require.Equal(t, 0, count(t, db, `SELECT count(*) FROM trip_media WHERE display_allowed`))
}

func TestRightsChangesPropagateToCatalogue(t *testing.T) {
	svc, db, clk := newTestIngest(t)
	putSource(t, svc, openSource())
	importListings(t, svc, true, testListing("a"))
	require.Equal(t, 1, count(t, db, `SELECT count(*) FROM trip_media WHERE display_allowed`))
	require.Equal(t, 1, count(t, db, `SELECT count(*) FROM trip_inclusions`))

	clk.Advance(time.Hour)
	narrowed := openSource()
	narrowed.ImageAllowed, narrowed.DescriptionAllowed = false, false
	putSource(t, svc, narrowed)
	require.Equal(t, 0, count(t, db, `SELECT count(*) FROM trip_media WHERE display_allowed`))
	require.Nil(t, getTrip(t, db, "a").Description)
	require.Equal(t, 0, count(t, db, `SELECT count(*) FROM trip_inclusions`))
	require.Equal(t, 0, count(t, db, `SELECT count(*) FROM trip_itinerary_days WHERE description IS NOT NULL`))
	require.Equal(t, "published", getTrip(t, db, "a").Status, "losing text and image rights does not unpublish facts")

	clk.Advance(time.Hour)
	research := narrowed
	research.RightsBasis, research.AutoPublish = BasisResearchOnly, false
	putSource(t, svc, research)
	a := getTrip(t, db, "a")
	require.Equal(t, "draft", a.Status)
	require.Equal(t, "source rights do not allow publishing", *a.StatusReason)

	clk.Advance(time.Hour)
	putSource(t, svc, openSource())
	run := importListings(t, svc, true, testListing("a"))
	require.Equal(t, 1, run.UpdatedCount, "widened rights force a rewrite of unchanged listings")
	a = getTrip(t, db, "a")
	require.Equal(t, "About a", *a.Description)
	require.Equal(t, "draft", a.Status, "re-publishing after a rights downgrade is an admin decision")
	require.Equal(t, 1, count(t, db, `SELECT count(*) FROM trip_media WHERE display_allowed`))
}

func TestExpiredTripIsNotRevivedWithoutPublishRights(t *testing.T) {
	svc, db, clk := newTestIngest(t)
	in := openSource()
	in.MissingGraceRuns = 1
	putSource(t, svc, in)
	importListings(t, svc, true, testListing("a"), testListing("b"))
	clk.Advance(time.Hour)
	importListings(t, svc, true, testListing("a"))
	require.Equal(t, "expired", getTrip(t, db, "b").Status)

	clk.Advance(time.Hour)
	research := in
	research.RightsBasis, research.AutoPublish = BasisResearchOnly, false
	putSource(t, svc, research)
	importListings(t, svc, true, testListing("a"), testListing("b"))
	require.Equal(t, "draft", getTrip(t, db, "b").Status)
}

func TestImportRequiresKnownEnabledSource(t *testing.T) {
	svc, _, _ := newTestIngest(t)
	ctx := context.Background()

	_, err := svc.Import(ctx, testSource, Batch{Trigger: TriggerManual, Listings: []Listing{testListing("a")}})
	require.True(t, apperrors.IsCode(err, apperrors.CodeNotFound))

	in := openSource()
	in.Enabled = false
	putSource(t, svc, in)
	_, err = svc.Import(ctx, testSource, Batch{Trigger: TriggerManual, Listings: []Listing{testListing("a")}})
	require.True(t, apperrors.IsCode(err, apperrors.CodeConflict))

	_, err = svc.Import(ctx, testSource, Batch{Trigger: "cron", Listings: []Listing{testListing("a")}})
	require.True(t, apperrors.IsCode(err, apperrors.CodeInvalid))
	_, err = svc.Import(ctx, testSource, Batch{Trigger: TriggerManual})
	require.True(t, apperrors.IsCode(err, apperrors.CodeInvalid))
}

func TestConcurrentImportsForOneSourceSerialize(t *testing.T) {
	svc, db, _ := newTestIngest(t)
	putSource(t, svc, openSource())

	listings := []Listing{testListing("a"), testListing("b"), testListing("c")}
	runs := make([]Run, 4)
	errs := make([]error, len(runs))
	var wg sync.WaitGroup
	for i := range runs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runs[i], errs[i] = svc.Import(context.Background(), testSource, Batch{Trigger: TriggerScraper, Complete: true, Listings: listings})
		}()
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}

	var inserted, unchanged int
	for _, r := range runs {
		inserted += r.InsertedCount
		unchanged += r.UnchangedCount
		require.Zero(t, r.UpdatedCount)
	}
	require.Equal(t, 3, inserted, "exactly one run inserts each listing")
	require.Equal(t, 9, unchanged)
	require.Equal(t, 3, count(t, db, `SELECT count(*) FROM trips`))
}
