package ingest

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/adi6859/travel-swipe-backend/internal/platform/httpx"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newAdminRouter(t *testing.T) *gin.Engine {
	t.Helper()
	svc, _, _ := newTestIngest(t)
	r := gin.New()
	NewAdminHandler(svc, httpx.NewResponder(slog.New(slog.NewJSONHandler(io.Discard, nil)))).RegisterRoutes(r.Group("/admin/v1"))
	return r
}

func do(t *testing.T, r http.Handler, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	out := map[string]any{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return rec, out
}

const sourceJSON = `{
	"display_name": "Acme Treks feed",
	"base_url": "https://example.com",
	"auto_publish": true,
	"rights_basis": "contract",
	"description_allowed": true,
	"attribution_text": "Listing by Acme Treks",
	"evidence_ref": "Partner agreement 2026-09"
}`

const importJSON = `{
	"complete": true,
	"listings": [{
		"external_id": "kk",
		"source_url": "https://example.com/kk",
		"booking_url": "https://example.com/kk/book",
		"provider": {"slug": "acme", "name": "Acme Treks"},
		"title": "Kedarkantha",
		"destination": "Kedarkantha",
		"duration_days": 6,
		"duration_nights": 5,
		"departures": [{"start_date": "2026-12-20", "end_date": "2026-12-25", "price_inr": 9499}]
	}, {
		"external_id": "bad",
		"source_url": "https://example.com/bad",
		"booking_url": "https://example.com/bad",
		"provider": {"slug": "acme", "name": "Acme Treks"},
		"title": "",
		"destination": "Nowhere",
		"duration_days": 1,
		"duration_nights": 0
	}]
}`

func TestHTTPSourceLifecycleAndImport(t *testing.T) {
	r := newAdminRouter(t)

	rec, body := do(t, r, http.MethodPut, "/admin/v1/sources/acme_feed", sourceJSON)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "acme_feed", body["source_key"])
	require.Equal(t, true, body["enabled"], "enabled defaults to true")
	require.Equal(t, float64(2), body["missing_grace_runs"])

	rec, body = do(t, r, http.MethodGet, "/admin/v1/sources", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, body["sources"], 1)

	rec, body = do(t, r, http.MethodPost, "/admin/v1/sources/acme_feed/imports", importJSON)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "partial", body["status"])
	require.Equal(t, "manual", body["trigger"], "trigger defaults to manual")
	require.Equal(t, float64(1), body["inserted"])
	require.Equal(t, float64(1), body["rejected"])
	rejection := body["rejections"].([]any)[0].(map[string]any)
	require.Equal(t, float64(1), rejection["index"])
	require.Contains(t, rejection["errors"], "title")

	rec, body = do(t, r, http.MethodGet, "/admin/v1/sources/acme_feed/runs?limit=5", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, body["runs"], 1)
}

func TestHTTPAdminValidation(t *testing.T) {
	r := newAdminRouter(t)

	rec, body := do(t, r, http.MethodPut, "/admin/v1/sources/Bad-Key", `{"display_name":"x","base_url":"ftp://x","rights_basis":"scraped","evidence_ref":""}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	details := body["error"].(map[string]any)["details"].(map[string]any)
	for _, field := range []string{"source_key", "base_url", "rights_basis", "evidence_ref"} {
		require.Contains(t, details, field)
	}

	rec, _ = do(t, r, http.MethodPut, "/admin/v1/sources/acme_feed", `{"display_name":"x","unknown":1}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	rec, _ = do(t, r, http.MethodPost, "/admin/v1/sources/missing/imports", importJSON)
	require.Equal(t, http.StatusNotFound, rec.Code)

	rec, _ = do(t, r, http.MethodGet, "/admin/v1/sources/acme_feed/runs?limit=0", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
