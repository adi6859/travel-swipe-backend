package middleware

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/adi6859/travel-swipe-backend/internal/platform/httpx"
	"github.com/adi6859/travel-swipe-backend/pkg/ctxutil"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestRequestIDPropagatesValidInboundID(t *testing.T) {
	t.Parallel()

	r := gin.New()
	r.Use(RequestID())
	var seen string
	r.GET("/", func(c *gin.Context) { seen = ctxutil.RequestID(c.Request.Context()) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, "client-req-0001")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, "client-req-0001", seen)
	require.Equal(t, "client-req-0001", rec.Header().Get(RequestIDHeader))
}

func TestRequestIDReplacesMalformedInboundID(t *testing.T) {
	t.Parallel()

	r := gin.New()
	r.Use(RequestID())
	r.GET("/", func(*gin.Context) {})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, "bad id\nwith-injection")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	got := rec.Header().Get(RequestIDHeader)
	require.NotEqual(t, "bad id\nwith-injection", got)
	require.Len(t, got, 36)
}

func TestRecoveryReturnsEnvelope(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	r := gin.New()
	r.Use(RequestID(), Recovery(log, httpx.NewResponder(log)))
	r.GET("/", func(*gin.Context) { panic("kaboom") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Contains(t, rec.Body.String(), `"code":"internal"`)
	require.NotContains(t, rec.Body.String(), "kaboom")
	require.Contains(t, logs.String(), "panic recovered")
}

func TestRequestLoggerOmitsSecrets(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	r := gin.New()
	r.Use(RequestLogger(log))
	r.POST("/auth/refresh", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "/auth/refresh?token=qs-secret", strings.NewReader(`{"refresh_token":"body-secret"}`))
	req.Header.Set("Authorization", "Bearer header-secret")
	r.ServeHTTP(httptest.NewRecorder(), req)

	out := logs.String()
	require.Contains(t, out, `"route":"/auth/refresh"`)
	for _, secret := range []string{"qs-secret", "body-secret", "header-secret"} {
		require.NotContains(t, out, secret)
	}
}

func TestBodyLimitRejectsOversizedBody(t *testing.T) {
	t.Parallel()

	r := gin.New()
	r.Use(BodyLimit(8))
	var readErr error
	r.POST("/", func(c *gin.Context) { _, readErr = io.ReadAll(c.Request.Body) })

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", strings.NewReader("0123456789")))
	require.Error(t, readErr)
}

func TestSecurityHeaders(t *testing.T) {
	t.Parallel()

	r := gin.New()
	r.Use(SecurityHeaders())
	r.GET("/", func(*gin.Context) {})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
}
