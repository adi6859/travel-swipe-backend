package auth

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/adi6859/travel-swipe-backend/internal/platform/httpx"
	"github.com/adi6859/travel-swipe-backend/internal/platform/ratelimit"
)

func init() {
	gin.SetMode(gin.TestMode)
	httpx.SetupValidator()
}

type apiHarness struct {
	*harness
	router *gin.Engine
}

func newAPI(t *testing.T, limit gin.HandlerFunc) *apiHarness {
	t.Helper()
	h := newHarness(t)
	responder := httpx.NewResponder(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if limit == nil {
		limit = ratelimit.Disabled()
	}
	handler := NewHandler(h.svc, responder, limit)

	r := gin.New()
	api := r.Group("/api/v1")
	handler.RegisterRoutes(api)
	api.GET("/whoami", handler.RequireAuthMiddleware(), func(c *gin.Context) {
		p, _ := PrincipalFrom(c)
		c.JSON(http.StatusOK, gin.H{"user_id": p.UserID.String(), "session_id": p.SessionID.String()})
	})
	return &apiHarness{harness: h, router: r}
}

func (a *apiHarness) do(t *testing.T, method, path, body, bearer string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	a.router.ServeHTTP(rec, req)

	out := map[string]any{}
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	}
	return rec, out
}

func errorCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func TestHTTPFullAuthFlow(t *testing.T) {
	a := newAPI(t, nil)

	rec, body := a.do(t, http.MethodPost, "/api/v1/auth/otp/request", `{"phone":"`+testPhone+`"}`, "")
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	require.NotEmpty(t, body["challenge_id"])
	require.NotEmpty(t, body["resend_after"])

	rec, body = a.do(t, http.MethodPost, "/api/v1/auth/otp/verify",
		`{"phone":"`+testPhone+`","code":"`+a.sms.lastCode(t)+`","device_name":"Pixel 9"}`, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, true, body["is_new_user"])
	require.Equal(t, "Bearer", body["token_type"])
	user := body["user"].(map[string]any)
	require.Equal(t, testPhone, user["phone"])
	access := body["access_token"].(string)
	refresh := body["refresh_token"].(string)

	rec, body = a.do(t, http.MethodGet, "/api/v1/whoami", "", access)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, user["id"], body["user_id"])

	rec, body = a.do(t, http.MethodPost, "/api/v1/auth/refresh", `{"refresh_token":"`+refresh+`"}`, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	newAccess := body["access_token"].(string)
	require.NotEqual(t, refresh, body["refresh_token"])

	rec, _ = a.do(t, http.MethodPost, "/api/v1/auth/logout", "", newAccess)
	require.Equal(t, http.StatusNoContent, rec.Code)

	rec, body = a.do(t, http.MethodGet, "/api/v1/whoami", "", newAccess)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "SESSION_REVOKED", errorCode(body))
}

func TestHTTPLogoutAll(t *testing.T) {
	a := newAPI(t, nil)
	first := a.login(t, testPhone)
	a.login(t, testPhone)

	rec, body := a.do(t, http.MethodPost, "/api/v1/auth/logout-all", "", first.Tokens.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)
	require.EqualValues(t, 2, body["revoked_sessions"])
}

func TestHTTPValidationErrorsHaveFieldDetails(t *testing.T) {
	a := newAPI(t, nil)

	rec, body := a.do(t, http.MethodPost, "/api/v1/auth/otp/request", `{"phone":"98765"}`, "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid_argument", errorCode(body))
	details := body["error"].(map[string]any)["details"].(map[string]any)
	require.Contains(t, details, "phone")

	rec, body = a.do(t, http.MethodPost, "/api/v1/auth/otp/verify", `{"phone":"`+testPhone+`","code":"abc"}`, "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, body["error"].(map[string]any)["details"], "code")
}

func TestHTTPInvalidOTPStatus(t *testing.T) {
	a := newAPI(t, nil)
	a.do(t, http.MethodPost, "/api/v1/auth/otp/request", `{"phone":"`+testPhone+`"}`, "")

	rec, body := a.do(t, http.MethodPost, "/api/v1/auth/otp/verify",
		`{"phone":"`+testPhone+`","code":"`+wrongCode(a.sms.lastCode(t))+`"}`, "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "OTP_INVALID", errorCode(body))
}

func TestHTTPResendCooldownIs429(t *testing.T) {
	a := newAPI(t, nil)
	a.do(t, http.MethodPost, "/api/v1/auth/otp/request", `{"phone":"`+testPhone+`"}`, "")

	rec, body := a.do(t, http.MethodPost, "/api/v1/auth/otp/request", `{"phone":"`+testPhone+`"}`, "")
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Equal(t, true, body["error"].(map[string]any)["retryable"])
}

func TestHTTPMiddlewareRejections(t *testing.T) {
	a := newAPI(t, nil)
	login := a.login(t, testPhone)

	cases := []struct {
		name   string
		header string
		code   string
	}{
		{"missing header", "", "ACCESS_TOKEN_INVALID"},
		{"wrong scheme", "Basic " + login.Tokens.AccessToken, "ACCESS_TOKEN_INVALID"},
		{"raw token without scheme", login.Tokens.AccessToken, "ACCESS_TOKEN_INVALID"},
		{"empty bearer", "Bearer ", "ACCESS_TOKEN_INVALID"},
		{"garbage", "Bearer not.a.jwt", "ACCESS_TOKEN_INVALID"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil)
		if tc.header != "" {
			req.Header.Set("Authorization", tc.header)
		}
		rec := httptest.NewRecorder()
		a.router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code, tc.name)
		require.Contains(t, rec.Body.String(), tc.code, tc.name)
	}

	a.clock.Advance(16 * time.Minute)
	rec, body := a.do(t, http.MethodGet, "/api/v1/whoami", "", login.Tokens.AccessToken)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "ACCESS_TOKEN_EXPIRED", errorCode(body))
}

func TestHTTPLogoutRequiresAuth(t *testing.T) {
	a := newAPI(t, nil)
	rec, _ := a.do(t, http.MethodPost, "/api/v1/auth/logout", "", "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHTTPPublicRoutesAreRateLimited(t *testing.T) {
	responder := httpx.NewResponder(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	a := newAPI(t, ratelimit.PerIP("auth", ratelimit.New(2, time.Minute), responder))

	for range 2 {
		rec, _ := a.do(t, http.MethodPost, "/api/v1/auth/refresh", `{"refresh_token":"x"}`, "")
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	}
	rec, body := a.do(t, http.MethodPost, "/api/v1/auth/refresh", `{"refresh_token":"x"}`, "")
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Equal(t, "too_many_requests", errorCode(body))
	require.NotEmpty(t, rec.Header().Get("Retry-After"))
}
