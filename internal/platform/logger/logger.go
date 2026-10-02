// Package logger builds the structured JSON logger and enriches records with
// request-scoped identifiers carried in context.
package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/adi6859/travel-swipe-backend/pkg/ctxutil"
)

// contextHandler adds request_id / user_id / session_id from context to every record.
type contextHandler struct {
	handler slog.Handler
}

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *contextHandler) Handle(ctx context.Context, record slog.Record) error {
	if v := ctxutil.RequestID(ctx); v != "" {
		record.AddAttrs(slog.String("request_id", v))
	}
	if v := ctxutil.UserID(ctx); v != "" {
		record.AddAttrs(slog.String("user_id", v))
	}
	if v := ctxutil.SessionID(ctx); v != "" {
		record.AddAttrs(slog.String("session_id", v))
	}
	return h.handler.Handle(ctx, record)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{handler: h.handler.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{handler: h.handler.WithGroup(name)}
}

// New returns a JSON logger writing to stdout.
func New(service, env, level string) *slog.Logger {
	return NewWithWriter(service, env, level, os.Stdout)
}

func NewWithWriter(service, env, level string, w io.Writer) *slog.Logger {
	base := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: ParseLevel(level)})
	return slog.New(&contextHandler{handler: base}).With("service", service, "env", env)
}

func ParseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// MaskPhone keeps only the last four digits of a phone number for logging.
func MaskPhone(phone string) string {
	if len(phone) <= 4 {
		return "****"
	}
	return "****" + phone[len(phone)-4:]
}
