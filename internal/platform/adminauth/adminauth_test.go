package adminauth

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/adi6859/travel-swipe-backend/internal/platform/httpx"
)

func TestRequire(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sum := sha256.Sum256([]byte("correct-horse-battery-staple"))
	mw, err := Require(hex.EncodeToString(sum[:]), httpx.NewResponder(slog.New(slog.NewJSONHandler(io.Discard, nil))))
	require.NoError(t, err)

	r := gin.New()
	r.GET("/x", mw, func(c *gin.Context) { c.Status(http.StatusNoContent) })

	cases := map[string]int{
		"Bearer correct-horse-battery-staple": http.StatusNoContent,
		"bearer correct-horse-battery-staple": http.StatusNoContent,
		"Bearer wrong":                        http.StatusUnauthorized,
		"Bearer ":                             http.StatusUnauthorized,
		"Basic correct-horse-battery-staple":  http.StatusUnauthorized,
		"Bearer correct-horse battery":        http.StatusUnauthorized,
		"":                                    http.StatusUnauthorized,
	}
	for header, want := range cases {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equal(t, want, rec.Code, "header %q", header)
	}
}

func TestRequireRejectsBadHash(t *testing.T) {
	_, err := Require("abc", httpx.NewResponder(slog.New(slog.NewJSONHandler(io.Discard, nil))))
	require.Error(t, err)
}
