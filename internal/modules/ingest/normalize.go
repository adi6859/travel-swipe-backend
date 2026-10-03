package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxListingsPerBatch = 200
	maxImages           = 20
	maxItineraryDays    = 60
	maxInclusions       = 50
	maxDepartures       = 200
	maxSummaryRunes     = 300
	maxDescriptionRunes = 10000
	maxDayTitleRunes    = 200
	maxDayDescRunes     = 2000
	maxInclusionRunes   = 300
	dateLayout          = "2006-01-02"
)

var (
	whitespace   = regexp.MustCompile(`\s+`)
	providerSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,59}$`)
	countryCode  = regexp.MustCompile(`^[A-Z]{2}$`)
	difficulties = map[string]bool{"easy": true, "moderate": true, "difficult": true, "challenging": true}
	availability = map[string]bool{"available": true, "filling_fast": true, "waitlist": true, "sold_out": true, "unknown": true}
)

// normalized is a validated listing ready to persist.
type normalized struct {
	Listing
	ContentHash string
	Departures  []normalizedDeparture
}

type normalizedDeparture struct {
	Key                string
	ExternalID         string
	StartDate          time.Time
	EndDate            time.Time
	DepartureCity      *string
	PricePaise         *int64
	OriginalPricePaise *int64
	Availability       string
	SeatsLeft          *int
}

// normalize cleans l and validates it against the contract. All problems are
// reported together, keyed by JSON field path.
func normalize(l Listing, interests map[string]bool) (normalized, map[string]string) {
	errs := map[string]string{}
	fail := func(field, msg string) {
		if _, exists := errs[field]; !exists {
			errs[field] = msg
		}
	}

	l.ExternalID = clean(l.ExternalID)
	if n := utf8.RuneCountInString(l.ExternalID); n == 0 || n > 200 {
		fail("external_id", "is required and must be at most 200 characters")
	}
	l.SourceURL = strings.TrimSpace(l.SourceURL)
	if !isHTTPURL(l.SourceURL, false) {
		fail("source_url", "must be an absolute http(s) URL")
	}
	l.BookingURL = strings.TrimSpace(l.BookingURL)
	if !isHTTPURL(l.BookingURL, true) {
		fail("booking_url", "must be an absolute https URL")
	}

	l.Provider.Slug = strings.ToLower(clean(l.Provider.Slug))
	if !providerSlug.MatchString(l.Provider.Slug) {
		fail("provider.slug", "must be 2-60 lowercase letters, digits or dashes")
	}
	l.Provider.Name = clean(l.Provider.Name)
	if n := utf8.RuneCountInString(l.Provider.Name); n == 0 || n > 100 {
		fail("provider.name", "is required and must be at most 100 characters")
	}
	l.Provider.WebsiteURL = strings.TrimSpace(l.Provider.WebsiteURL)
	if l.Provider.WebsiteURL != "" && !isHTTPURL(l.Provider.WebsiteURL, false) {
		fail("provider.website_url", "must be an absolute http(s) URL")
	}
	checkRating("provider.rating", l.Provider.Rating, fail)
	checkNonNegative("provider.review_count", l.Provider.ReviewCount, fail)

	l.Title = clean(l.Title)
	if n := utf8.RuneCountInString(l.Title); n == 0 || n > 150 {
		fail("title", "is required and must be at most 150 characters")
	}
	l.Summary = truncate(clean(l.Summary), maxSummaryRunes)
	l.Description = truncate(cleanMultiline(l.Description), maxDescriptionRunes)

	l.Destination = clean(l.Destination)
	if n := utf8.RuneCountInString(l.Destination); n == 0 || n > 100 {
		fail("destination", "is required and must be at most 100 characters")
	}
	l.Region = clean(l.Region)
	if utf8.RuneCountInString(l.Region) > 100 {
		fail("region", "must be at most 100 characters")
	}
	l.CountryCode = strings.ToUpper(clean(l.CountryCode))
	if l.CountryCode == "" {
		l.CountryCode = "IN"
	}
	if !countryCode.MatchString(l.CountryCode) {
		fail("country_code", "must be an ISO 3166-1 alpha-2 code")
	}
	switch {
	case (l.Latitude == nil) != (l.Longitude == nil):
		fail("latitude", "latitude and longitude must be given together")
	case l.Latitude != nil && (*l.Latitude < -90 || *l.Latitude > 90 || *l.Longitude < -180 || *l.Longitude > 180):
		fail("latitude", "coordinates are out of range")
	}

	if l.DurationDays < 1 || l.DurationDays > 60 {
		fail("duration_days", "must be between 1 and 60")
	}
	if l.DurationNights < 0 || l.DurationNights > l.DurationDays {
		fail("duration_nights", "must be between 0 and duration_days")
	}
	l.Difficulty = strings.ToLower(clean(l.Difficulty))
	if l.Difficulty != "" && !difficulties[l.Difficulty] {
		fail("difficulty", "must be one of: easy, moderate, difficult, challenging")
	}
	if l.MaxGroupSize != nil && (*l.MaxGroupSize < 1 || *l.MaxGroupSize > 200) {
		fail("max_group_size", "must be between 1 and 200")
	}
	if l.MinAge != nil && (*l.MinAge < 0 || *l.MinAge > 99) {
		fail("min_age", "must be between 0 and 99")
	}
	checkRating("rating", l.Rating, fail)
	checkNonNegative("review_count", l.ReviewCount, fail)

	l.Interests = dedupeLower(l.Interests)
	for _, slug := range l.Interests {
		if !interests[slug] {
			fail("interests", "unknown interest "+strconv.Quote(slug))
		}
	}

	if len(l.Images) > maxImages {
		fail("images", fmt.Sprintf("must contain at most %d images", maxImages))
	}
	seenImages := map[string]bool{}
	images := l.Images[:0:0]
	for i, img := range l.Images {
		img.URL = strings.TrimSpace(img.URL)
		if !isHTTPURL(img.URL, true) {
			fail(fmt.Sprintf("images[%d].url", i), "must be an absolute https URL")
		}
		if (img.Width != nil && *img.Width <= 0) || (img.Height != nil && *img.Height <= 0) {
			fail(fmt.Sprintf("images[%d]", i), "width and height must be positive")
		}
		if !seenImages[img.URL] {
			seenImages[img.URL] = true
			images = append(images, img)
		}
	}
	l.Images = images

	if len(l.Itinerary) > maxItineraryDays {
		fail("itinerary", fmt.Sprintf("must contain at most %d days", maxItineraryDays))
	}
	seenDays := map[int]bool{}
	for i := range l.Itinerary {
		d := &l.Itinerary[i]
		d.Title = truncate(clean(d.Title), maxDayTitleRunes)
		d.Description = truncate(cleanMultiline(d.Description), maxDayDescRunes)
		if d.Day < 1 || d.Day > maxItineraryDays || seenDays[d.Day] {
			fail(fmt.Sprintf("itinerary[%d].day", i), "must be a unique day number between 1 and 60")
		}
		if d.Title == "" {
			fail(fmt.Sprintf("itinerary[%d].title", i), "is required")
		}
		seenDays[d.Day] = true
	}

	l.Inclusions = cleanList(l.Inclusions)
	l.Exclusions = cleanList(l.Exclusions)
	if len(l.Inclusions) > maxInclusions || len(l.Exclusions) > maxInclusions {
		fail("inclusions", fmt.Sprintf("inclusions and exclusions may each have at most %d entries", maxInclusions))
	}

	if len(l.Departures) > maxDepartures {
		fail("departures", fmt.Sprintf("must contain at most %d departures", maxDepartures))
	}
	departures := make([]normalizedDeparture, 0, len(l.Departures))
	seenKeys := map[string]bool{}
	for i, d := range l.Departures {
		field := func(name string) string { return fmt.Sprintf("departures[%d].%s", i, name) }
		nd, ok := normalizeDeparture(d, field, fail)
		if !ok {
			continue
		}
		if seenKeys[nd.Key] {
			fail(field("start_date"), "duplicates another departure")
			continue
		}
		seenKeys[nd.Key] = true
		departures = append(departures, nd)
	}

	if len(errs) > 0 {
		return normalized{}, errs
	}
	return normalized{Listing: l, ContentHash: contentHash(l), Departures: departures}, nil
}

func normalizeDeparture(d Departure, field func(string) string, fail func(string, string)) (normalizedDeparture, bool) {
	ok := true
	bad := func(name, msg string) { fail(field(name), msg); ok = false }

	start, err := time.Parse(dateLayout, strings.TrimSpace(d.StartDate))
	if err != nil {
		bad("start_date", "must be a date in YYYY-MM-DD format")
	}
	end, err := time.Parse(dateLayout, strings.TrimSpace(d.EndDate))
	if err != nil {
		bad("end_date", "must be a date in YYYY-MM-DD format")
	} else if ok && end.Before(start) {
		bad("end_date", "must not be before start_date")
	}

	nd := normalizedDeparture{
		ExternalID:   clean(d.ExternalID),
		StartDate:    start,
		EndDate:      end,
		Availability: strings.ToLower(clean(d.Availability)),
		SeatsLeft:    d.SeatsLeft,
	}
	if nd.Availability == "" {
		nd.Availability = "unknown"
	}
	if !availability[nd.Availability] {
		bad("availability", "must be one of: available, filling_fast, waitlist, sold_out, unknown")
	}
	if city := clean(d.DepartureCity); city != "" {
		if utf8.RuneCountInString(city) > 100 {
			bad("departure_city", "must be at most 100 characters")
		}
		nd.DepartureCity = &city
	}
	if d.SeatsLeft != nil && *d.SeatsLeft < 0 {
		bad("seats_left", "must not be negative")
	}
	if nd.PricePaise, err = toPaise(d.PriceINR); err != nil {
		bad("price_inr", err.Error())
	}
	if nd.OriginalPricePaise, err = toPaise(d.OriginalPriceINR); err != nil {
		bad("original_price_inr", err.Error())
	}
	if nd.PricePaise != nil && nd.OriginalPricePaise != nil && *nd.OriginalPricePaise < *nd.PricePaise {
		bad("original_price_inr", "must not be lower than price_inr")
	}
	if utf8.RuneCountInString(nd.ExternalID) > 200 {
		bad("external_id", "must be at most 200 characters")
	}

	if nd.ExternalID != "" {
		nd.Key = "id:" + nd.ExternalID
	} else {
		city := ""
		if nd.DepartureCity != nil {
			city = strings.ToLower(*nd.DepartureCity)
		}
		nd.Key = "date:" + start.Format(dateLayout) + "|" + city
	}
	return nd, ok
}

func toPaise(inr *float64) (*int64, error) {
	if inr == nil {
		return nil, nil
	}
	if math.IsNaN(*inr) || *inr < 0 || *inr > 10_000_000 {
		return nil, fmt.Errorf("must be between 0 and 10000000")
	}
	p := int64(math.Round(*inr * 100))
	return &p, nil
}

// contentHash identifies the listing's content; unchanged listings skip the
// catalogue rewrite.
func contentHash(l Listing) string {
	data, _ := json.Marshal(l)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func isHTTPURL(raw string, httpsOnly bool) bool {
	if raw == "" || len(raw) > 2048 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	if httpsOnly {
		return u.Scheme == "https"
	}
	return u.Scheme == "https" || u.Scheme == "http"
}

// clean collapses whitespace and strips control characters.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.TrimSpace(whitespace.ReplaceAllString(s, " "))
}

// cleanMultiline keeps paragraph breaks but normalizes everything else.
func cleanMultiline(s string) string {
	paragraphs := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := paragraphs[:0]
	for _, p := range paragraphs {
		if p = clean(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n")
}

func cleanList(items []string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		if s = truncate(clean(s), maxInclusionRunes); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func dedupeLower(items []string) []string {
	out := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, s := range items {
		s = strings.ToLower(clean(s))
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:max-1])) + "…"
}

func checkRating(field string, v *float64, fail func(string, string)) {
	if v != nil && (math.IsNaN(*v) || *v < 0 || *v > 5) {
		fail(field, "must be between 0 and 5")
	}
}

func checkNonNegative(field string, v *int, fail func(string, string)) {
	if v != nil && *v < 0 {
		fail(field, "must not be negative")
	}
}
