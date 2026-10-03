package catalog

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/adi6859/travel-swipe-backend/internal/platform/httpx"
	"github.com/adi6859/travel-swipe-backend/pkg/ctxutil"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// fakeAuth mimics auth.RequireAuth: "Bearer <user-id>" authenticates that user.
func fakeAuth(responder *httpx.Responder) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if _, err := uuid.Parse(id); err != nil {
			responder.Abort(c, apperrors.AccessTokenInvalid("missing or malformed bearer token"))
			return
		}
		c.Request = c.Request.WithContext(ctxutil.WithUserID(c.Request.Context(), id))
		c.Next()
	}
}

func newHTTPFixture(t *testing.T) (fixture, *gin.Engine, uuid.UUID) {
	t.Helper()
	f := newFixture(t)
	userID := uuid.Must(uuid.NewV7())
	_, err := f.db.Exec(`INSERT INTO users (id, phone_e164, phone_verified_at) VALUES ($1, '+919800000002', now())`, userID)
	require.NoError(t, err)

	responder := httpx.NewResponder(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	r := gin.New()
	NewHandler(f.svc, responder, fakeAuth(responder)).RegisterRoutes(r.Group("/api/v1"))
	NewAdminHandler(f.svc, responder).RegisterRoutes(r.Group("/admin/v1"))
	return f, r, userID
}

func call(t *testing.T, r http.Handler, method, path, body string, userID uuid.UUID) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if userID != uuid.Nil {
		req.Header.Set("Authorization", "Bearer "+userID.String())
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	out := map[string]any{}
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	}
	return rec, out
}

func errorDetails(body map[string]any) map[string]any {
	e, _ := body["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	return d
}

func TestHTTPTripsRequireAuth(t *testing.T) {
	_, r, _ := newHTTPFixture(t)
	rec, _ := call(t, r, http.MethodGet, "/api/v1/trips", "", uuid.Nil)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHTTPListAndPaginate(t *testing.T) {
	_, r, user := newHTTPFixture(t)

	rec, body := call(t, r, http.MethodGet, "/api/v1/trips?limit=2", "", user)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	trips := body["trips"].([]any)
	require.Len(t, trips, 2)
	kk := trips[1].(map[string]any)
	require.Equal(t, "Kedarkantha", kk["destination"])
	require.Equal(t, "2026-12-20", kk["next_departure_date"])
	require.Equal(t, float64(949900), kk["min_price_paise"])
	require.Equal(t, "INR", kk["currency"])
	require.Equal(t, map[string]any{"name": "Acme Treks", "slug": "acme"}, kk["provider"])
	require.Equal(t, []any{"mountains", "trekking"}, kk["interests"])
	require.NotContains(t, kk, "sort_key")

	cursor := body["next_cursor"].(string)
	rec, body = call(t, r, http.MethodGet, "/api/v1/trips?limit=2&cursor="+url.QueryEscape(cursor), "", user)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "Gokarna", body["trips"].([]any)[0].(map[string]any)["destination"])

	rec, body = call(t, r, http.MethodGet, "/api/v1/trips?limit=50", "", user)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Nil(t, body["next_cursor"])
}

func TestHTTPListQueryValidation(t *testing.T) {
	_, r, user := newHTTPFixture(t)
	rec, body := call(t, r, http.MethodGet, "/api/v1/trips?month=2027-13&min_days=abc&max_price_inr=x&limit=0", "", user)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	details := errorDetails(body)
	for _, field := range []string{"month", "min_days", "max_price_inr", "limit"} {
		require.Contains(t, details, field)
	}

	rec, body = call(t, r, http.MethodGet, "/api/v1/trips?sort=newest", "", user)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, errorDetails(body), "sort")

	rec, body = call(t, r, http.MethodGet, "/api/v1/trips?q=kedar&month=2026-12&departure_city=Dehradun&max_price_inr=9500", "", user)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, body["trips"], 1)
}

func TestHTTPCategories(t *testing.T) {
	_, r, user := newHTTPFixture(t)
	rec, body := call(t, r, http.MethodGet, "/api/v1/trips/categories", "", user)
	require.Equal(t, http.StatusOK, rec.Code)
	cats := body["categories"].([]any)
	require.Len(t, cats, 6)
	require.Equal(t, map[string]any{"key": "treks", "label": "Treks", "count": float64(3)}, cats[0])
}

func TestHTTPTripDetail(t *testing.T) {
	f, r, user := newHTTPFixture(t)
	id := f.id(t, "kk")

	rec, body := call(t, r, http.MethodGet, "/api/v1/trips/"+id.String(), "", user)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, true, body["bookable"])
	require.Equal(t, map[string]any{"latitude": 31.02, "longitude": 78.17}, body["coordinates"])
	require.Equal(t, "https://example.com/acme", body["provider"].(map[string]any)["website_url"])
	require.Len(t, body["images"], 2)
	require.Equal(t, []any{"Meals", "Tents"}, body["included"])
	first := body["departures"].([]any)[0].(map[string]any)
	require.Equal(t, "2026-12-20", first["start_date"])
	require.Equal(t, float64(949900), first["price_paise"])
	require.Equal(t, "filling_fast", first["availability"])
	require.NotContains(t, body, "booking_url", "booking links are only handed out via outbound")

	rec, _ = call(t, r, http.MethodGet, "/api/v1/trips/not-a-uuid", "", user)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHTTPOutbound(t *testing.T) {
	f, r, user := newHTTPFixture(t)
	path := "/api/v1/trips/" + f.id(t, "kk").String() + "/outbound"

	rec, body := call(t, r, http.MethodPost, path, `{}`, user)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "https://example.com/kk/book", body["url"])
	require.Equal(t, 1, countRows(t, f.db, `SELECT count(*) FROM trip_outbound_clicks WHERE user_id = $1`, user))

	rec, body = call(t, r, http.MethodPost, path, `{"departure_id":"nope"}`, user)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, errorDetails(body), "departure_id")

	rec, _ = call(t, r, http.MethodPost, path, `{"url":"https://evil.example"}`, user)
	require.Equal(t, http.StatusBadRequest, rec.Code, "unknown fields are rejected")
}

func TestHTTPAdminModeration(t *testing.T) {
	f, r, _ := newHTTPFixture(t)
	id := f.id(t, "kk").String()

	rec, body := call(t, r, http.MethodGet, "/admin/v1/trips?status=published&source="+fixtureSource, "", uuid.Nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, body["trips"], 6)

	rec, _ = call(t, r, http.MethodPost, "/admin/v1/trips/"+id+"/hide", `{"reason":"duplicate listing"}`, uuid.Nil)
	require.Equal(t, http.StatusNoContent, rec.Code)
	rec, _ = call(t, r, http.MethodPost, "/admin/v1/trips/"+id+"/hide", `{"reason":"x","force":true}`, uuid.Nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	rec, _ = call(t, r, http.MethodPost, "/admin/v1/trips/"+id+"/publish", "", uuid.Nil)
	require.Equal(t, http.StatusNoContent, rec.Code)
	rec, _ = call(t, r, http.MethodGet, "/admin/v1/trips?limit=500", "", uuid.Nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
