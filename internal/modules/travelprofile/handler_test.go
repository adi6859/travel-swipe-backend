package travelprofile

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

func newTestRouter(t *testing.T) (*gin.Engine, *fakeStore) {
	t.Helper()
	svc, store, _ := newTestService()
	responder := httpx.NewResponder(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	r := gin.New()
	NewHandler(svc, responder, fakeAuth(responder)).RegisterRoutes(r.Group("/api/v1"))
	return r, store
}

func send(t *testing.T, r http.Handler, method, path, body string, userID uuid.UUID) (*httptest.ResponseRecorder, map[string]any) {
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
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return rec, out
}

const profilePath = "/api/v1/users/me/travel-profile"

func TestHTTPGetEmptyProfile(t *testing.T) {
	r, store := newTestRouter(t)

	rec, body := send(t, r, http.MethodGet, profilePath, "", store.addUser())
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []any{}, body["travel_styles"], "lists serialize as [] not null")
	require.Equal(t, []any{}, body["interests"])
	require.Equal(t, []any{}, body["languages"])
	require.Contains(t, body, "budget_band")
	require.Nil(t, body["budget_band"])
	require.Nil(t, body["updated_at"])
	require.Equal(t, false, body["complete"])
}

func TestHTTPPutThenGet(t *testing.T) {
	r, store := newTestRouter(t)
	id := store.addUser()

	rec, body := send(t, r, http.MethodPut, profilePath, `{
		"travel_styles": ["backpacker"],
		"interests": ["trekking", "beaches", "heritage"],
		"languages": ["en", "hi"],
		"budget_band": "budget",
		"group_size": "small",
		"pace": "balanced",
		"smoking": "never",
		"drinking": "sometimes",
		"diet": "vegetarian"
	}`, id)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, true, body["complete"])
	require.Equal(t, "2026-10-02T09:00:00Z", body["updated_at"])

	rec, body = send(t, r, http.MethodGet, profilePath, "", id)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []any{"trekking", "beaches", "heritage"}, body["interests"])
	require.Equal(t, "vegetarian", body["diet"])
}

func TestHTTPPutRejectsBadPayloads(t *testing.T) {
	r, store := newTestRouter(t)
	id := store.addUser()

	cases := map[string]struct {
		body  string
		field string
	}{
		"unknown field":     {`{"user_id":"x"}`, "user_id"},
		"list wrong type":   {`{"interests":"trekking"}`, "interests"},
		"scalar wrong type": {`{"pace":3}`, "pace"},
		"invalid value":     {`{"diet":"keto"}`, "diet"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec, body := send(t, r, http.MethodPut, profilePath, tc.body, id)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			details := body["error"].(map[string]any)["details"].(map[string]any)
			require.Contains(t, details, tc.field)
		})
	}

	rec, _ := send(t, r, http.MethodPut, profilePath, `{"pace":"relaxed"}{}`, id)
	require.Equal(t, http.StatusBadRequest, rec.Code, "trailing JSON is rejected")
}

func TestHTTPOptions(t *testing.T) {
	r, store := newTestRouter(t)

	rec, body := send(t, r, http.MethodGet, "/api/v1/travel-profile/options", "", store.addUser())
	require.Equal(t, http.StatusOK, rec.Code)
	for _, key := range []string{"travel_styles", "interests", "languages", "budget_bands", "group_sizes", "paces", "smoking", "drinking", "diets"} {
		require.NotEmpty(t, body[key], key)
	}
	first := body["interests"].([]any)[0].(map[string]any)
	require.Equal(t, map[string]any{"slug": "trekking", "label": "Trekking", "category": "outdoors"}, first)
	style := body["travel_styles"].([]any)[0].(map[string]any)
	require.Equal(t, map[string]any{"value": "backpacker", "label": "Backpacker"}, style)
}

func TestHTTPRequiresAuth(t *testing.T) {
	r, _ := newTestRouter(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, profilePath},
		{http.MethodPut, profilePath},
		{http.MethodGet, "/api/v1/travel-profile/options"},
	} {
		rec, _ := send(t, r, tc.method, tc.path, `{}`, uuid.Nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code, tc.method+" "+tc.path)
	}
}

func TestHTTPPutUnknownUser(t *testing.T) {
	r, _ := newTestRouter(t)
	rec, _ := send(t, r, http.MethodPut, profilePath, `{}`, uuid.Must(uuid.NewV7()))
	require.Equal(t, http.StatusNotFound, rec.Code)
}
