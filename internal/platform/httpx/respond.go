// Package httpx holds the HTTP transport contract: the error envelope, status
// mapping, and request binding helpers shared by every module.
package httpx

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/adi6859/travel-swipe-backend/pkg/ctxutil"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
)

// HandlerFunc is a Gin handler that returns an error instead of writing it.
type HandlerFunc func(c *gin.Context) error

// ErrorResponse is the single error envelope returned by the API.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Details   map[string]string `json:"details,omitempty"`
	RequestID string            `json:"request_id,omitempty"`
	Retryable bool              `json:"retryable"`
}

// Responder writes errors using a shared logger.
type Responder struct {
	logger *slog.Logger
}

func NewResponder(logger *slog.Logger) *Responder {
	return &Responder{logger: logger}
}

// Handle adapts an error-returning handler to gin.HandlerFunc.
func (r *Responder) Handle(fn HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := fn(c); err != nil {
			r.Abort(c, err)
		}
	}
}

// Abort logs err, writes the error envelope, and stops the handler chain.
//
// Only messages explicitly marked safe reach clients; causes and stack traces
// stay in server logs.
func (r *Responder) Abort(c *gin.Context, err error) {
	r.AbortWithStatus(c, StatusOf(err), err)
}

// AbortWithStatus is Abort with an explicit HTTP status, for transport-level
// failures (e.g. 405) that have no dedicated application code.
func (r *Responder) AbortWithStatus(c *gin.Context, status int, err error) {
	ctx := c.Request.Context()
	if status >= http.StatusInternalServerError {
		r.logger.ErrorContext(ctx, "request error", apperrors.LogFields(err)...)
	} else {
		r.logger.WarnContext(ctx, "request error", "error", err, "code", apperrors.CodeOf(err))
	}

	if c.Writer.Written() {
		c.Abort()
		return
	}
	c.AbortWithStatusJSON(status, ErrorResponse{Error: ErrorBody{
		Code:      string(apperrors.CodeOf(err)),
		Message:   apperrors.SafeMessage(err),
		Details:   apperrors.Details(err),
		RequestID: ctxutil.RequestID(ctx),
		Retryable: status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable,
	}})
}

// StatusOf maps an application error code to its HTTP status.
func StatusOf(err error) int {
	switch apperrors.CodeOf(err) {
	case apperrors.CodeInvalid:
		return http.StatusBadRequest
	case apperrors.CodeUnauthorized,
		apperrors.CodeAccessTokenInvalid,
		apperrors.CodeAccessTokenExpired,
		apperrors.CodeRefreshTokenInvalid,
		apperrors.CodeSessionRevoked,
		apperrors.CodeOTPInvalid,
		apperrors.CodeOTPExpired:
		return http.StatusUnauthorized
	case apperrors.CodeForbidden, apperrors.CodeAccountSuspended:
		return http.StatusForbidden
	case apperrors.CodeNotFound:
		return http.StatusNotFound
	case apperrors.CodeConflict:
		return http.StatusConflict
	case apperrors.CodeTooManyRequests, apperrors.CodeOTPAttemptsExceeded:
		return http.StatusTooManyRequests
	case apperrors.CodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
