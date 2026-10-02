// Package middleware contains cross-cutting HTTP middleware.
package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/adi6859/travel-swipe-backend/internal/platform/httpx"
	"github.com/adi6859/travel-swipe-backend/pkg/ctxutil"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
)

const RequestIDHeader = "X-Request-ID"

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

// RequestID propagates a well-formed inbound X-Request-ID or generates one.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDHeader)
		if !validRequestID.MatchString(id) {
			id = uuid.NewString()
		}
		c.Header(RequestIDHeader, id)
		c.Request = c.Request.WithContext(ctxutil.WithRequestID(c.Request.Context(), id))
		c.Next()
	}
}

// Recovery converts panics into a logged 500 with the standard error envelope.
func Recovery(logger *slog.Logger, responder *httpx.Responder) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(c.Request.Context(), "panic recovered",
					"panic", fmt.Sprint(recovered),
					"stack_trace", string(debug.Stack()),
				)
				responder.Abort(c, apperrors.Internal("panic", fmt.Errorf("%v", recovered)))
			}
		}()
		c.Next()
	}
}

// RequestLogger emits one structured line per request. It deliberately logs no
// headers, query strings, or bodies so credentials and tokens never reach logs.
func RequestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := c.Writer.Status()
		level := slog.LevelInfo
		switch {
		case status >= http.StatusInternalServerError:
			level = slog.LevelError
		case status >= http.StatusBadRequest:
			level = slog.LevelWarn
		}
		logger.Log(c.Request.Context(), level, "http request",
			"method", c.Request.Method,
			"route", route,
			"status", status,
			"latency_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
			"bytes_out", c.Writer.Size(),
		)
	}
}

// BodyLimit caps request body size.
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

// SecurityHeaders sets conservative defaults for a JSON API.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		c.Next()
	}
}
