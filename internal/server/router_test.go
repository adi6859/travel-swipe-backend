package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

func TestWrongMethodReturns405Envelope(t *testing.T) {
	rec := serve(newTestRouter(t, fakePinger{}), http.MethodDelete, "/healthz")
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	require.Contains(t, rec.Body.String(), `"message":"method not allowed"`)
}
