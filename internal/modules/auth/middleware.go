package auth

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/adi6859/travel-swipe-backend/internal/platform/httpx"
	"github.com/adi6859/travel-swipe-backend/pkg/ctxutil"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
)

const principalKey = "auth.principal"

// Authenticator validates access tokens.
type Authenticator interface {
	Authenticate(ctx context.Context, accessToken string) (Principal, error)
}

// RequireAuth rejects requests without a valid bearer token for a live session.
func RequireAuth(a Authenticator, responder *httpx.Responder) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			responder.Abort(c, apperrors.AccessTokenInvalid("missing or malformed bearer token"))
			return
		}
		p, err := a.Authenticate(c.Request.Context(), token)
		if err != nil {
			responder.Abort(c, err)
			return
		}
		ctx := ctxutil.WithUserID(c.Request.Context(), p.UserID.String())
		ctx = ctxutil.WithSessionID(ctx, p.SessionID.String())
		c.Request = c.Request.WithContext(ctx)
		c.Set(principalKey, p)
		c.Next()
	}
}

// PrincipalFrom returns the caller set by RequireAuth.
func PrincipalFrom(c *gin.Context) (Principal, bool) {
	v, ok := c.Get(principalKey)
	if !ok {
		return Principal{}, false
	}
	p, ok := v.(Principal)
	return p, ok
}

// MustPrincipal returns the caller or an unauthorized error when the route was
// mounted without RequireAuth.
func MustPrincipal(c *gin.Context) (Principal, error) {
	p, ok := PrincipalFrom(c)
	if !ok {
		return Principal{}, apperrors.Unauthorized("authentication required")
	}
	return p, nil
}

func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}
