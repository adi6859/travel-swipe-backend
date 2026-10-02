package users

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
	svc, store := newTestService()
	responder := httpx.NewResponder(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	r := gin.New()
	NewHandler(svc, responder, fakeAuth(responder)).RegisterRoutes(r.Group("/api/v1"))
	return r, store
}

func send(t *testing.T, r http.Handler, method, body string, userID uuid.UUID) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "/api/v1/users/me", rdr)
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

func TestHTTPGetMe(t *testing.T) {
	r, store := newTestRouter(t)
	id := store.add()

	rec, body := send(t, r, http.MethodGet, "", id)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, id.String(), body["id"])
	require.Equal(t, "+919876543210", body["phone"])
	require.Equal(t, false, body["profile_complete"])
	profile := body["profile"].(map[string]any)
	require.Contains(t, profile, "avatar")
	require.Nil(t, profile["avatar"])
	require.Nil(t, profile["date_of_birth"])
}

func TestHTTPPatchMe(t *testing.T) {
	r, store := newTestRouter(t)
	id := store.add()

	rec, body := send(t, r, http.MethodPatch, `{
		"display_name": "Asha",
		"date_of_birth": "1998-04-15",
		"avatar": {"url":"https://cdn.example.com/a.jpg","width":512,"height":512,"mime_type":"image/jpeg","size_bytes":2048},
		"country_code": "IN"
	}`, id)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, true, body["profile_complete"])
	profile := body["profile"].(map[string]any)
	require.Equal(t, "Asha", profile["display_name"])
	require.Equal(t, "1998-04-15", profile["date_of_birth"])
	require.Equal(t, "https://cdn.example.com/a.jpg", profile["avatar"].(map[string]any)["url"])
	require.EqualValues(t, 512, profile["avatar"].(map[string]any)["width"])

	rec, body = send(t, r, http.MethodPatch, `{"avatar": null}`, id)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Nil(t, body["profile"].(map[string]any)["avatar"])
	require.Equal(t, "Asha", body["profile"].(map[string]any)["display_name"])
}

func TestHTTPPatchMeRejectsCredentialAndUnknownFields(t *testing.T) {
	r, store := newTestRouter(t)
	id := store.add()

	for _, field := range []string{"phone", "status", "id", "displayName"} {
		rec, body := send(t, r, http.MethodPatch, `{"`+field+`":"x"}`, id)
		require.Equal(t, http.StatusBadRequest, rec.Code, field)
		details := body["error"].(map[string]any)["details"].(map[string]any)
		require.Equal(t, "is not a recognized field", details[field], field)
	}
	require.Zero(t, store.updates)
}

func TestHTTPPatchMeBadPayloads(t *testing.T) {
	r, store := newTestRouter(t)
	id := store.add()

	rec, body := send(t, r, http.MethodPatch, `{"display_name": 42}`, id)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, body["error"].(map[string]any)["details"], "display_name")

	rec, _ = send(t, r, http.MethodPatch, `{"display_name":"a"} {"x":1}`, id)
	require.Equal(t, http.StatusBadRequest, rec.Code, "trailing JSON is rejected")

	rec, _ = send(t, r, http.MethodPatch, `{}`, id)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	rec, body = send(t, r, http.MethodPatch, `{"date_of_birth":"2015-01-01"}`, id)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "you must be at least 18 years old",
		body["error"].(map[string]any)["details"].(map[string]any)["date_of_birth"])
}

func TestHTTPMeRequiresAuth(t *testing.T) {
	r, _ := newTestRouter(t)
	for _, m := range []string{http.MethodGet, http.MethodPatch} {
		rec, body := send(t, r, m, `{"bio":"x"}`, uuid.Nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code, m)
		require.Equal(t, "ACCESS_TOKEN_INVALID", body["error"].(map[string]any)["code"])
	}
}

func TestHTTPMeUnknownUser(t *testing.T) {
	r, _ := newTestRouter(t)
	rec, _ := send(t, r, http.MethodGet, "", uuid.New())
	require.Equal(t, http.StatusNotFound, rec.Code)
}
