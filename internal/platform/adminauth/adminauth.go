// Package adminauth guards internal admin routes with a static bearer token.
// Only the token's SHA-256 is configured, so the plaintext never sits in
// config or memory dumps of the config.
package adminauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/adi6859/travel-swipe-backend/internal/platform/httpx"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
)

// Require returns middleware that accepts only "Authorization: Bearer <token>"
// where sha256(token) equals tokenSHA256Hex.
func Require(tokenSHA256Hex string, responder *httpx.Responder) (gin.HandlerFunc, error) {
	want, err := hex.DecodeString(tokenSHA256Hex)
	if err != nil || len(want) != sha256.Size {
		return nil, fmt.Errorf("adminauth: token hash must be 64 hex characters")
	}
	return func(c *gin.Context) {
		token, ok := bearer(c.GetHeader("Authorization"))
		got := sha256.Sum256([]byte(token))
		if !ok || subtle.ConstantTimeCompare(got[:], want) != 1 {
			responder.Abort(c, apperrors.Unauthorized("admin authentication required"))
			return
		}
		c.Next()
	}, nil
}

func bearer(header string) (string, bool) {
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.ContainsAny(token, " \t") {
		return "", false
	}
	return token, true
}
