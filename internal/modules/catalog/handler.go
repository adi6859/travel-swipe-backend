package catalog

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/adi6859/travel-swipe-backend/internal/platform/httpx"
	"github.com/adi6859/travel-swipe-backend/pkg/ctxutil"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
)

const currencyINR = "INR"

type Handler struct {
	svc         *Service
	responder   *httpx.Responder
	requireAuth gin.HandlerFunc
}

// NewHandler takes the auth middleware, which must place the caller's user ID
// in the request context (ctxutil.WithUserID).
func NewHandler(svc *Service, responder *httpx.Responder, requireAuth gin.HandlerFunc) *Handler {
	return &Handler{svc: svc, responder: responder, requireAuth: requireAuth}
}

func (h *Handler) RegisterRoutes(api *gin.RouterGroup) {
	trips := api.Group("/trips", h.requireAuth)
	trips.GET("", h.responder.Handle(h.list))
	trips.GET("/categories", h.responder.Handle(h.categories))
	trips.GET("/:id", h.responder.Handle(h.detail))
	trips.POST("/:id/outbound", h.responder.Handle(h.outbound))
}

type providerSummary struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type tripCardResponse struct {
	ID                string          `json:"id"`
	Slug              string          `json:"slug"`
	Title             string          `json:"title"`
	Destination       string          `json:"destination"`
	Region            *string         `json:"region"`
	CountryCode       string          `json:"country_code"`
	DurationDays      int             `json:"duration_days"`
	DurationNights    int             `json:"duration_nights"`
	Difficulty        *string         `json:"difficulty"`
	Rating            *float64        `json:"rating"`
	ReviewCount       *int            `json:"review_count"`
	MaxGroupSize      *int            `json:"max_group_size"`
	Provider          providerSummary `json:"provider"`
	NextDepartureDate *string         `json:"next_departure_date"`
	MinPricePaise     *int64          `json:"min_price_paise"`
	Currency          string          `json:"currency"`
	CoverImageURL     *string         `json:"cover_image_url"`
	Interests         []string        `json:"interests"`
}

type providerResponse struct {
	Name        string   `json:"name"`
	Slug        string   `json:"slug"`
	WebsiteURL  *string  `json:"website_url"`
	Rating      *float64 `json:"rating"`
	ReviewCount *int     `json:"review_count"`
}

type coordinates struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type imageResponse struct {
	URL    string `json:"url"`
	Width  *int   `json:"width"`
	Height *int   `json:"height"`
}

type itineraryDayResponse struct {
	Day         int     `json:"day"`
	Title       string  `json:"title"`
	Description *string `json:"description"`
}

type departureResponse struct {
	ID                 string  `json:"id"`
	StartDate          string  `json:"start_date"`
	EndDate            string  `json:"end_date"`
	DepartureCity      *string `json:"departure_city"`
	PricePaise         *int64  `json:"price_paise"`
	OriginalPricePaise *int64  `json:"original_price_paise"`
	Currency           string  `json:"currency"`
	Availability       string  `json:"availability"`
	SeatsLeft          *int    `json:"seats_left"`
}

type tripDetailResponse struct {
	ID             string                 `json:"id"`
	Slug           string                 `json:"slug"`
	Title          string                 `json:"title"`
	Summary        *string                `json:"summary"`
	Description    *string                `json:"description"`
	Destination    string                 `json:"destination"`
	Region         *string                `json:"region"`
	CountryCode    string                 `json:"country_code"`
	Coordinates    *coordinates           `json:"coordinates"`
	DurationDays   int                    `json:"duration_days"`
	DurationNights int                    `json:"duration_nights"`
	Difficulty     *string                `json:"difficulty"`
	MaxGroupSize   *int                   `json:"max_group_size"`
	MinAge         *int                   `json:"min_age"`
	Rating         *float64               `json:"rating"`
	ReviewCount    *int                   `json:"review_count"`
	Status         string                 `json:"status"`
	Bookable       bool                   `json:"bookable"`
	Attribution    *string                `json:"attribution"`
	Provider       providerResponse       `json:"provider"`
	Interests      []string               `json:"interests"`
	Images         []imageResponse        `json:"images"`
	Itinerary      []itineraryDayResponse `json:"itinerary"`
	Included       []string               `json:"included"`
	Excluded       []string               `json:"excluded"`
	Departures     []departureResponse    `json:"departures"`
}

type outboundRequest struct {
	DepartureID *string `json:"departure_id"`
}

func (h *Handler) list(c *gin.Context) error {
	f, err := parseFilter(c)
	if err != nil {
		return err
	}
	page, err := h.svc.ListTrips(c.Request.Context(), f)
	if err != nil {
		return err
	}
	out := make([]tripCardResponse, 0, len(page.Trips))
	for _, t := range page.Trips {
		out = append(out, toCardResponse(t))
	}
	var next *string
	if page.NextCursor != "" {
		next = &page.NextCursor
	}
	c.JSON(http.StatusOK, gin.H{"trips": out, "next_cursor": next})
	return nil
}

func (h *Handler) categories(c *gin.Context) error {
	out, err := h.svc.Categories(c.Request.Context())
	if err != nil {
		return err
	}
	c.JSON(http.StatusOK, gin.H{"categories": out})
	return nil
}

func (h *Handler) detail(c *gin.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return apperrors.NotFound("trip not found")
	}
	t, err := h.svc.Trip(c.Request.Context(), id)
	if err != nil {
		return err
	}
	c.JSON(http.StatusOK, toDetailResponse(t))
	return nil
}

func (h *Handler) outbound(c *gin.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return apperrors.NotFound("trip not found")
	}
	var req outboundRequest
	if err := httpx.DecodeStrictJSON(c, &req); err != nil {
		return err
	}
	var departureID *uuid.UUID
	if req.DepartureID != nil {
		d, err := uuid.Parse(*req.DepartureID)
		if err != nil {
			return apperrors.InvalidFields("request validation failed", map[string]string{"departure_id": "must be a UUID"})
		}
		departureID = &d
	}
	var userID *uuid.UUID
	if u, err := uuid.Parse(ctxutil.UserID(c.Request.Context())); err == nil {
		userID = &u
	}
	url, err := h.svc.Outbound(c.Request.Context(), id, departureID, userID)
	if err != nil {
		return err
	}
	c.JSON(http.StatusOK, gin.H{"url": url})
	return nil
}

func parseFilter(c *gin.Context) (Filter, error) {
	fields := map[string]string{}
	f := Filter{
		Query:         c.Query("q"),
		Category:      c.Query("category"),
		Interest:      c.Query("interest"),
		Difficulty:    c.Query("difficulty"),
		DepartureCity: c.Query("departure_city"),
		Sort:          Sort(c.Query("sort")),
		Cursor:        c.Query("cursor"),
	}
	intParam := func(name string) *int {
		raw := c.Query(name)
		if raw == "" {
			return nil
		}
		n, err := strconv.Atoi(raw)
		if err != nil {
			fields[name] = "must be an integer"
			return nil
		}
		return &n
	}
	f.MinDays = intParam("min_days")
	f.MaxDays = intParam("max_days")
	if n := intParam("limit"); n != nil {
		if *n == 0 {
			fields["limit"] = "must be between 1 and 50"
		}
		f.Limit = *n
	}
	if n := intParam("max_price_inr"); n != nil {
		if *n > math.MaxInt32 {
			fields["max_price_inr"] = "is too large"
		}
		paise := int64(*n) * 100
		f.MaxPricePaise = &paise
	}
	if raw := c.Query("month"); raw != "" {
		m, err := time.Parse("2006-01", raw)
		if err != nil {
			fields["month"] = "must be YYYY-MM"
		} else {
			f.Month = &m
		}
	}
	if len(fields) > 0 {
		return Filter{}, apperrors.InvalidFields("request validation failed", fields)
	}
	return f, nil
}

func dateString(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(dateLayout)
	return &s
}

func toCardResponse(t TripCard) tripCardResponse {
	return tripCardResponse{
		ID: t.ID.String(), Slug: t.Slug, Title: t.Title, Destination: t.Destination, Region: t.Region,
		CountryCode: t.CountryCode, DurationDays: t.DurationDays, DurationNights: t.DurationNights,
		Difficulty: t.Difficulty, Rating: t.Rating, ReviewCount: t.ReviewCount, MaxGroupSize: t.MaxGroupSize,
		Provider:          providerSummary{Name: t.ProviderName, Slug: t.ProviderSlug},
		NextDepartureDate: dateString(t.NextDeparture), MinPricePaise: t.MinPricePaise, Currency: currencyINR,
		CoverImageURL: t.CoverURL, Interests: t.Interests,
	}
}

func toDetailResponse(t TripDetail) tripDetailResponse {
	out := tripDetailResponse{
		ID: t.ID.String(), Slug: t.Slug, Title: t.Title, Summary: t.Summary, Description: t.Description,
		Destination: t.Destination, Region: t.Region, CountryCode: t.CountryCode,
		DurationDays: t.DurationDays, DurationNights: t.DurationNights, Difficulty: t.Difficulty,
		MaxGroupSize: t.MaxGroupSize, MinAge: t.MinAge, Rating: t.Rating, ReviewCount: t.ReviewCount,
		Status: t.Status, Bookable: t.Status == "published", Attribution: t.AttributionText,
		Provider: providerResponse{
			Name: t.Provider.Name, Slug: t.Provider.Slug, WebsiteURL: t.Provider.WebsiteURL,
			Rating: t.Provider.Rating, ReviewCount: t.Provider.ReviewCount,
		},
		Interests:  t.Interests,
		Images:     make([]imageResponse, 0, len(t.Media)),
		Itinerary:  make([]itineraryDayResponse, 0, len(t.Itinerary)),
		Included:   t.Included,
		Excluded:   t.Excluded,
		Departures: make([]departureResponse, 0, len(t.Departures)),
	}
	if t.Latitude != nil && t.Longitude != nil {
		out.Coordinates = &coordinates{Latitude: *t.Latitude, Longitude: *t.Longitude}
	}
	for _, m := range t.Media {
		out.Images = append(out.Images, imageResponse(m))
	}
	for _, d := range t.Itinerary {
		out.Itinerary = append(out.Itinerary, itineraryDayResponse(d))
	}
	for _, d := range t.Departures {
		out.Departures = append(out.Departures, departureResponse{
			ID: d.ID.String(), StartDate: d.StartDate.Format(dateLayout), EndDate: d.EndDate.Format(dateLayout),
			DepartureCity: d.DepartureCity, PricePaise: d.PricePaise, OriginalPricePaise: d.OriginalPricePaise,
			Currency: currencyINR, Availability: d.Availability, SeatsLeft: d.SeatsLeft,
		})
	}
	return out
}

// AdminHandler exposes trip moderation on /admin/v1. Callers mount it behind
// admin authentication.
type AdminHandler struct {
	svc       *Service
	responder *httpx.Responder
}

func NewAdminHandler(svc *Service, responder *httpx.Responder) *AdminHandler {
	return &AdminHandler{svc: svc, responder: responder}
}

func (h *AdminHandler) RegisterRoutes(admin *gin.RouterGroup) {
	admin.GET("/trips", h.responder.Handle(h.list))
	admin.POST("/trips/:id/publish", h.responder.Handle(h.publish))
	admin.POST("/trips/:id/hide", h.responder.Handle(h.hide))
}

type hideRequest struct {
	Reason string `json:"reason"`
}

func (h *AdminHandler) list(c *gin.Context) error {
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			return apperrors.InvalidFields("request validation failed", map[string]string{"limit": "must be between 1 and 200"})
		}
		limit = n
	}
	trips, err := h.svc.AdminList(c.Request.Context(), c.Query("status"), c.Query("source"), limit)
	if err != nil {
		return err
	}
	for i := range trips {
		trips[i].UpdatedAt = trips[i].UpdatedAt.UTC()
		if trips[i].PublishedAt != nil {
			at := trips[i].PublishedAt.UTC()
			trips[i].PublishedAt = &at
		}
	}
	c.JSON(http.StatusOK, gin.H{"trips": trips})
	return nil
}

func (h *AdminHandler) publish(c *gin.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return apperrors.NotFound("trip not found")
	}
	if err := h.svc.Publish(c.Request.Context(), id); err != nil {
		return err
	}
	c.Status(http.StatusNoContent)
	return nil
}

func (h *AdminHandler) hide(c *gin.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return apperrors.NotFound("trip not found")
	}
	var req hideRequest
	if err := httpx.DecodeStrictJSON(c, &req); err != nil {
		return err
	}
	if err := h.svc.Hide(c.Request.Context(), id, req.Reason); err != nil {
		return err
	}
	c.Status(http.StatusNoContent)
	return nil
}
