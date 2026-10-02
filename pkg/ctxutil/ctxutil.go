// Package ctxutil carries request-scoped identifiers through context.Context.
package ctxutil

import "context"

type ctxKey int

const (
	requestIDKey ctxKey = iota
	userIDKey
	sessionIDKey
)

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

func WithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, userIDKey, id)
}

func UserID(ctx context.Context) string {
	v, _ := ctx.Value(userIDKey).(string)
	return v
}

func WithSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sessionIDKey, id)
}

func SessionID(ctx context.Context) string {
	v, _ := ctx.Value(sessionIDKey).(string)
	return v
}
