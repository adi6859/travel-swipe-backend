package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/adi6859/travel-swipe-backend/internal/config"
	"github.com/adi6859/travel-swipe-backend/internal/platform/httpx"
)

type fakePinger struct{ err error }

func (f fakePinger) PingContext(context.Context) error { return f.err }

type echoModule struct{}

func (echoModule) RegisterRoutes(api *gin.RouterGroup) {
	api.GET("/echo", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
}

func newTestRouter(t *testing.T, db Pinger) *gin.Engine {
	t.Helper()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := &config.Config{
		App:  config.AppConfig{Env: config.EnvTest},
		HTTP: config.HTTPConfig{MaxBodyBytes: 1 << 20},
	}
	r, err := NewRouter(Deps{
		Config:    cfg,
		Logger:    log,
		Responder: httpx.NewResponder(log),
		DB:        db,
		Modules:   []Module{echoModule{}},
	})
	require.NoError(t, err)
	return r
}

func serve(r http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestHealthz(t *testing.T) {
	rec := serve(newTestRouter(t, fakePinger{}), http.MethodGet, "/healthz")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotEmpty(t, rec.Header().Get("X-Request-ID"))
}

func TestReadyz(t *testing.T) {
	rec := serve(newTestRouter(t, fakePinger{}), http.MethodGet, "/readyz")
	require.Equal(t, http.StatusOK, rec.Code)

	rec = serve(newTestRouter(t, fakePinger{err: errors.New("down")}), http.MethodGet, "/readyz")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), `"code":"unavailable"`)
	require.Contains(t, rec.Body.String(), `"retryable":true`)
}

func TestModulesMountUnderAPIV1(t *testing.T) {
	rec := serve(newTestRouter(t, fakePinger{}), http.MethodGet, "/api/v1/echo")
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestUnknownRouteUsesEnvelope(t *testing.T) {
	rec := serve(newTestRouter(t, fakePinger{}), http.MethodGet, "/nope")
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), `"code":"not_found"`)
}

// bodyModule reports how many body bytes it could read.
type bodyModule struct{}

func (bodyModule) RegisterRoutes(g *gin.RouterGroup) {
	g.POST("/body", func(c *gin.Context) {
		data, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		c.JSON(http.StatusOK, gin.H{"bytes": len(data)})
	})
}

func newAdminRouter(t *testing.T, adminAuth gin.HandlerFunc) *gin.Engine {
	t.Helper()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r, err := NewRouter(Deps{
		Config: &config.Config{
			App:  config.AppConfig{Env: config.EnvTest},
			HTTP: config.HTTPConfig{MaxBodyBytes: 10, AdminMaxBodyBytes: 100},
		},
		Logger:       log,
		Responder:    httpx.NewResponder(log),
		DB:           fakePinger{},
		Modules:      []Module{bodyModule{}},
		AdminModules: []Module{bodyModule{}},
		AdminAuth:    adminAuth,
	})
	require.NoError(t, err)
	return r
}

func post(r http.Handler, path string, size int, header string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(strings.Repeat("x", size)))
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestAdminGroupHasItsOwnAuthAndBodyLimit(t *testing.T) {
	auth := func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer admin" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	}
	r := newAdminRouter(t, auth)

	require.Equal(t, http.StatusOK, post(r, "/api/v1/body", 10, "").Code)
	require.Equal(t, http.StatusRequestEntityTooLarge, post(r, "/api/v1/body", 11, "").Code)

	require.Equal(t, http.StatusUnauthorized, post(r, "/admin/v1/body", 10, "").Code)
	rec := post(r, "/admin/v1/body", 100, "Bearer admin")
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"bytes":100}`, rec.Body.String())
	require.Equal(t, http.StatusRequestEntityTooLarge, post(r, "/admin/v1/body", 101, "Bearer admin").Code)
}

func TestAdminRoutesAbsentWithoutAdminAuth(t *testing.T) {
	r := newAdminRouter(t, nil)
	rec := post(r, "/admin/v1/body", 1, "Bearer admin")
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), `"code":"not_found"`)
}

func TestWrongMethodReturns405Envelope(t *testing.T) {
	rec := serve(newTestRouter(t, fakePinger{}), http.MethodDelete, "/healthz")
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	require.Contains(t, rec.Body.String(), `"message":"method not allowed"`)
}
