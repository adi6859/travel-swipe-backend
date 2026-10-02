package httpx

import (
	"bytes"
	"encoding/json"
	stdErrors "errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/adi6859/travel-swipe-backend/pkg/ctxutil"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
)

func init() {
	gin.SetMode(gin.TestMode)
	SetupValidator()
}

func newResponder(w io.Writer) *Responder {
	return NewResponder(slog.New(slog.NewJSONHandler(w, nil)))
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) ErrorResponse {
	t.Helper()
	var out ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

func TestStatusMapping(t *testing.T) {
	t.Parallel()

	cases := map[apperrors.Code]int{
		apperrors.CodeInvalid:             http.StatusBadRequest,
		apperrors.CodeUnauthorized:        http.StatusUnauthorized,
		apperrors.CodeAccessTokenInvalid:  http.StatusUnauthorized,
		apperrors.CodeAccessTokenExpired:  http.StatusUnauthorized,
		apperrors.CodeRefreshTokenInvalid: http.StatusUnauthorized,
		apperrors.CodeSessionRevoked:      http.StatusUnauthorized,
		apperrors.CodeOTPInvalid:          http.StatusUnauthorized,
		apperrors.CodeOTPExpired:          http.StatusUnauthorized,
		apperrors.CodeOTPAttemptsExceeded: http.StatusTooManyRequests,
		apperrors.CodeAccountSuspended:    http.StatusForbidden,
		apperrors.CodeForbidden:           http.StatusForbidden,
		apperrors.CodeNotFound:            http.StatusNotFound,
		apperrors.CodeConflict:            http.StatusConflict,
		apperrors.CodeTooManyRequests:     http.StatusTooManyRequests,
		apperrors.CodeUnavailable:         http.StatusServiceUnavailable,
		apperrors.CodeInternal:            http.StatusInternalServerError,
	}
	for code, want := range cases {
		require.Equal(t, want, StatusOf(apperrors.New(code, "x")), code)
	}
	require.Equal(t, http.StatusInternalServerError, StatusOf(stdErrors.New("raw")))
}

func TestAbortWritesEnvelopeAndHidesInternalCause(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request = req.WithContext(ctxutil.WithRequestID(req.Context(), "req-12345678"))

	newResponder(&logs).Abort(c, apperrors.Internal("query failed", stdErrors.New("password=hunter2")))

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	body := decode(t, rec)
	require.Equal(t, "internal", body.Error.Code)
	require.Equal(t, "internal error", body.Error.Message)
	require.Equal(t, "req-12345678", body.Error.RequestID)
	require.False(t, body.Error.Retryable)
	require.NotContains(t, rec.Body.String(), "hunter2")
	require.Contains(t, logs.String(), "stack_trace")
}

func TestAbortMarksThrottlingRetryable(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	newResponder(io.Discard).Abort(c, apperrors.TooManyRequests("slow down"))

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	body := decode(t, rec)
	require.True(t, body.Error.Retryable)
	require.Equal(t, "slow down", body.Error.Message)
}

type sampleRequest struct {
	Phone string `json:"phone" binding:"required,e164"`
	Name  string `json:"display_name" binding:"omitempty,max=5"`
}

func bindRequest(t *testing.T, body string) error {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	var dst sampleRequest
	return BindJSON(c, &dst)
}

func TestBindJSONReportsFieldDetailsByJSONName(t *testing.T) {
	t.Parallel()

	err := bindRequest(t, `{"phone":"98765","display_name":"toolongname"}`)
	require.Equal(t, apperrors.CodeInvalid, apperrors.CodeOf(err))
	require.Equal(t, map[string]string{
		"phone":        "must be an E.164 phone number, e.g. +919876543210",
		"display_name": "must be at most 5 characters",
	}, apperrors.Details(err))
}

func TestBindJSONMalformedAndEmpty(t *testing.T) {
	t.Parallel()

	err := bindRequest(t, `{"phone":`)
	require.Equal(t, "malformed JSON body", apperrors.SafeMessage(err))

	err = bindRequest(t, ``)
	require.Equal(t, "request body is required", apperrors.SafeMessage(err))
}

func decodeStrict(t *testing.T, body string) error {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(body))
	var dst struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	return DecodeStrictJSON(c, &dst)
}

func TestDecodeStrictJSON(t *testing.T) {
	t.Parallel()

	require.NoError(t, decodeStrict(t, `{"name":"a","age":3}`))

	err := decodeStrict(t, `{"name":"a","phone":"+91"}`)
	require.Equal(t, map[string]string{"phone": "is not a recognized field"}, apperrors.Details(err))

	err = decodeStrict(t, `{"age":"three"}`)
	require.Equal(t, map[string]string{"age": "has the wrong type"}, apperrors.Details(err))

	require.Equal(t, "malformed JSON body", apperrors.SafeMessage(decodeStrict(t, `{"name":"a"}{}`)))
	require.Equal(t, "malformed JSON body", apperrors.SafeMessage(decodeStrict(t, `{"name":`)))
	require.Equal(t, "request body is required", apperrors.SafeMessage(decodeStrict(t, ``)))
}

func TestBindJSONAccepts(t *testing.T) {
	t.Parallel()

	require.NoError(t, bindRequest(t, `{"phone":"+919876543210"}`))
}
