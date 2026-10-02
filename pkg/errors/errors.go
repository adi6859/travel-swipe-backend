// Package errors defines the application error contract used across layers.
//
// Strategy:
//   - Create typed AppError values with stable machine codes.
//   - Keep user-facing safety explicit (safe vs internal message).
//   - Preserve causes with wrapping for diagnostics and error chain checks.
//   - Capture stack at AppError creation for server-side debugging.
//
// Map errors to transport concerns (HTTP status) at the transport boundary only.
package errors

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
)

// Code represents a stable machine-readable error identifier.
type Code string

const (
	CodeInternal        Code = "internal"
	CodeInvalid         Code = "invalid_argument"
	CodeUnauthorized    Code = "unauthorized"
	CodeForbidden       Code = "forbidden"
	CodeNotFound        Code = "not_found"
	CodeConflict        Code = "conflict"
	CodeUnavailable     Code = "unavailable"
	CodeTooManyRequests Code = "too_many_requests"

	// Authentication failures use stable, client-actionable codes while still
	// mapping to the same transport-level unauthorized status.
	CodeAccessTokenInvalid  Code = "ACCESS_TOKEN_INVALID"
	CodeAccessTokenExpired  Code = "ACCESS_TOKEN_EXPIRED"
	CodeRefreshTokenInvalid Code = "REFRESH_TOKEN_INVALID"
	CodeSessionRevoked      Code = "SESSION_REVOKED"
	CodeAccountSuspended    Code = "ACCOUNT_SUSPENDED"
	CodeOTPInvalid          Code = "OTP_INVALID"
	CodeOTPExpired          Code = "OTP_EXPIRED"
	CodeOTPAttemptsExceeded Code = "OTP_ATTEMPTS_EXCEEDED"
)

// AppError wraps failures with codes, user-safety, and optional cause.
type AppError struct {
	code    Code
	message string
	safe    bool
	details map[string]string
	cause   error
	stack   []uintptr
}

// Option customizes AppError construction.
type Option func(*AppError)

// WithSafeMessage marks the message as safe to expose to clients.
func WithSafeMessage() Option {
	return func(e *AppError) { e.safe = true }
}

// WithCause attaches a wrapped error cause.
func WithCause(err error) Option {
	return func(e *AppError) { e.cause = err }
}

// WithDetails attaches client-safe, field-level details (e.g. validation failures).
func WithDetails(details map[string]string) Option {
	return func(e *AppError) { e.details = details }
}

// New builds a new AppError with the provided code and message.
func New(code Code, message string, opts ...Option) *AppError {
	err := &AppError{code: code, message: message, stack: captureStack(3)}
	for _, opt := range opts {
		opt(err)
	}
	return err
}

// Wrap annotates an existing error with a new code and message.
func Wrap(err error, code Code, message string, opts ...Option) *AppError {
	wrapped := append(opts, WithCause(err))
	return New(code, message, wrapped...)
}

func captureStack(skip int) []uintptr {
	pcs := make([]uintptr, 64)
	n := runtime.Callers(skip, pcs)
	if n == 0 {
		return nil
	}
	stack := make([]uintptr, n)
	copy(stack, pcs[:n])
	return stack
}

func (e *AppError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.cause != nil {
		return fmt.Sprintf("%s: %v", e.message, e.cause)
	}
	return e.message
}

// Unwrap exposes the error cause for errors.Is / errors.As.
func (e *AppError) Unwrap() error {
	return e.cause
}

// StackTrace renders the captured stack trace. Logs only; never expose in API responses.
func (e *AppError) StackTrace() string {
	if e == nil || len(e.stack) == 0 {
		return ""
	}
	frames := runtime.CallersFrames(e.stack)
	var b strings.Builder
	for {
		frame, more := frames.Next()
		if frame.Function != "" {
			b.WriteString(frame.Function)
		} else {
			b.WriteString("unknown")
		}
		fmt.Fprintf(&b, "\n\t%s:%d", frame.File, frame.Line)
		if !more {
			break
		}
		b.WriteString("\n")
	}
	return b.String()
}

// StackTrace returns the rendered stack trace of the first AppError in the chain.
func StackTrace(err error) string {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.StackTrace()
	}
	return ""
}

// CodeOf extracts the Code from any error in the chain.
func CodeOf(err error) Code {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.code
	}
	return CodeInternal
}

// SafeMessage returns a client-safe error message when available.
func SafeMessage(err error) string {
	var appErr *AppError
	if errors.As(err, &appErr) && appErr.safe && appErr.message != "" {
		return appErr.message
	}
	return "internal error"
}

// Details returns client-safe details attached to the error, if any.
func Details(err error) map[string]string {
	var appErr *AppError
	if errors.As(err, &appErr) && appErr.safe {
		return appErr.details
	}
	return nil
}

// IsCode compares error codes without exposing internals.
func IsCode(err error, code Code) bool {
	return CodeOf(err) == code
}

// LogFields returns common slog fields for error diagnostics.
func LogFields(err error) []any {
	fields := []any{"error", err}
	if st := StackTrace(err); st != "" {
		fields = append(fields, "stack_trace", st)
	}
	return fields
}

func Unauthorized(message string) *AppError {
	return New(CodeUnauthorized, message, WithSafeMessage())
}

func AccessTokenInvalid(message string) *AppError {
	return New(CodeAccessTokenInvalid, message, WithSafeMessage())
}

func AccessTokenExpired(message string) *AppError {
	return New(CodeAccessTokenExpired, message, WithSafeMessage())
}

func RefreshTokenInvalid(message string) *AppError {
	return New(CodeRefreshTokenInvalid, message, WithSafeMessage())
}

func Invalid(message string) *AppError {
	return New(CodeInvalid, message, WithSafeMessage())
}

func InvalidFields(message string, fields map[string]string) *AppError {
	return New(CodeInvalid, message, WithSafeMessage(), WithDetails(fields))
}

func NotFound(message string) *AppError {
	return New(CodeNotFound, message, WithSafeMessage())
}

func Forbidden(message string) *AppError {
	return New(CodeForbidden, message, WithSafeMessage())
}

func Conflict(message string) *AppError {
	return New(CodeConflict, message, WithSafeMessage())
}

func Internal(message string, err error) *AppError {
	return Wrap(err, CodeInternal, message)
}

func Unavailable(message string, err error) *AppError {
	return Wrap(err, CodeUnavailable, message, WithSafeMessage())
}

func TooManyRequests(message string) *AppError {
	return New(CodeTooManyRequests, message, WithSafeMessage())
}
