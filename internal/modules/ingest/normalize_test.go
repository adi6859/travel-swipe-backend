package ingest

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var testInterests = map[string]bool{"trekking": true, "mountains": true, "camping": true}

func fptr(v float64) *float64 { return &v }
func iptr(v int) *int         { return &v }

func validListing() Listing {
	return Listing{
		ExternalID:     "  kedarkantha ",
		SourceURL:      "https://example.com/treks/kedarkantha",
		BookingURL:     "https://example.com/treks/kedarkantha/book",
		Provider:       Provider{Slug: "Summit-Co", Name: " Summit  Co "},
		Title:          "  Kedarkantha\tWinter   Trek ",
		Destination:    "Kedarkantha",
		DurationDays:   6,
		DurationNights: 5,
		Difficulty:     "Moderate",
		Interests:      []string{"Trekking", "mountains", "trekking", " "},
		Departures: []Departure{
			{ExternalID: "d1", StartDate: "2026-12-20", EndDate: "2026-12-25", DepartureCity: "Dehradun", PriceINR: fptr(9499.5)},
			{StartDate: "2027-01-10", EndDate: "2027-01-15", DepartureCity: "  Dehradun "},
		},
	}
}

func TestNormalizeCleansValidListing(t *testing.T) {
	n, errs := normalize(validListing(), testInterests)
	require.Empty(t, errs)
	require.Equal(t, "kedarkantha", n.ExternalID)
	require.Equal(t, "summit-co", n.Provider.Slug)
	require.Equal(t, "Summit Co", n.Provider.Name)
	require.Equal(t, "Kedarkantha Winter Trek", n.Title)
	require.Equal(t, "IN", n.CountryCode, "country defaults to India")
	require.Equal(t, "moderate", n.Difficulty)
	require.Equal(t, []string{"trekking", "mountains"}, n.Interests)
	require.Len(t, n.ContentHash, 64)

	require.Len(t, n.Departures, 2)
	require.Equal(t, "id:d1", n.Departures[0].Key)
	require.Equal(t, int64(949950), *n.Departures[0].PricePaise)
	require.Equal(t, "unknown", n.Departures[0].Availability)
	require.Equal(t, "date:2027-01-10|dehradun", n.Departures[1].Key, "departures without an ID are keyed by date and city")
	require.Nil(t, n.Departures[1].PricePaise)
}

func TestNormalizeReportsAllErrors(t *testing.T) {
	l := validListing()
	l.BookingURL = "http://example.com/book"
	l.SourceURL = "javascript:alert(1)"
	l.Provider.Slug = "-"
	l.Title = ""
	l.CountryCode = "IND"
	l.Latitude = fptr(10)
	l.DurationNights = 7
	l.Difficulty = "extreme"
	l.Rating = fptr(5.5)
	l.Interests = []string{"skydiving"}
	l.Images = []Image{{URL: "http://example.com/a.jpg"}}
	l.Itinerary = []Day{{Day: 1, Title: "a"}, {Day: 1, Title: "b"}, {Day: 2}}
	l.Departures = []Departure{
		{StartDate: "2026-13-01", EndDate: "2026-12-01"},
		{StartDate: "2026-12-05", EndDate: "2026-12-01"},
		{StartDate: "2026-12-05", EndDate: "2026-12-06", PriceINR: fptr(100), OriginalPriceINR: fptr(50), Availability: "maybe"},
		{ExternalID: "x", StartDate: "2026-12-05", EndDate: "2026-12-06"},
		{ExternalID: "x", StartDate: "2026-12-07", EndDate: "2026-12-08"},
		{StartDate: "2026-12-09", EndDate: "2026-12-10", PriceINR: fptr(-1)},
	}

	_, errs := normalize(l, testInterests)
	for _, field := range []string{
		"booking_url", "source_url", "provider.slug", "title", "country_code", "latitude", "duration_nights",
		"difficulty", "rating", "interests", "images[0].url", "itinerary[1].day", "itinerary[2].title",
		"departures[0].start_date", "departures[1].end_date", "departures[2].original_price_inr",
		"departures[2].availability", "departures[4].start_date", "departures[5].price_inr",
	} {
		require.Contains(t, errs, field)
	}
	require.Contains(t, errs["interests"], `"skydiving"`)
}

func TestNormalizeTruncatesLongText(t *testing.T) {
	l := validListing()
	l.Summary = strings.Repeat("a", 400)
	l.Description = "first\r\n\r\n  second   para  \n" + strings.Repeat("b", 20000)
	l.Inclusions = []string{" meals ", "", strings.Repeat("c", 500)}

	n, errs := normalize(l, testInterests)
	require.Empty(t, errs)
	require.Equal(t, maxSummaryRunes, len([]rune(n.Summary)))
	require.True(t, strings.HasSuffix(n.Summary, "…"))
	require.True(t, strings.HasPrefix(n.Description, "first\nsecond para\n"))
	require.Equal(t, maxDescriptionRunes, len([]rune(n.Description)))
	require.Len(t, n.Inclusions, 2)
	require.Equal(t, "meals", n.Inclusions[0])
	require.Equal(t, maxInclusionRunes, len([]rune(n.Inclusions[1])))
}

func TestNormalizeDeduplicatesImages(t *testing.T) {
	l := validListing()
	l.Images = []Image{{URL: "https://cdn.example.com/a.jpg"}, {URL: " https://cdn.example.com/a.jpg "}, {URL: "https://cdn.example.com/b.jpg", Width: iptr(800)}}
	n, errs := normalize(l, testInterests)
	require.Empty(t, errs)
	require.Len(t, n.Images, 2)
}

func TestContentHashTracksNormalizedContent(t *testing.T) {
	a, _ := normalize(validListing(), testInterests)

	spaced := validListing()
	spaced.Title = "Kedarkantha Winter Trek"
	b, _ := normalize(spaced, testInterests)
	require.Equal(t, a.ContentHash, b.ContentHash, "whitespace-only differences do not change the hash")

	changed := validListing()
	changed.Departures[0].PriceINR = fptr(9999)
	c, _ := normalize(changed, testInterests)
	require.NotEqual(t, a.ContentHash, c.ContentHash)
}

func TestToPaise(t *testing.T) {
	p, err := toPaise(fptr(12.34))
	require.NoError(t, err)
	require.Equal(t, int64(1234), *p)

	p, err = toPaise(nil)
	require.NoError(t, err)
	require.Nil(t, p)

	_, err = toPaise(fptr(10_000_001))
	require.Error(t, err)
}
