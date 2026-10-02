// Package ratelimit provides an in-process fixed-window limiter. Limits are per
// instance; swap in a shared (e.g. Redis) implementation when running replicas
// that must share counters.
package ratelimit

import (
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/adi6859/travel-swipe-backend/internal/platform/httpx"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
)

type window struct {
	count   int
	resetAt time.Time
}

type Limiter struct {
	mu        sync.Mutex
	limit     int
	period    time.Duration
	windows   map[string]*window
	lastSweep time.Time
	now       func() time.Time
}

func New(limit int, period time.Duration) *Limiter {
	return &Limiter{limit: limit, period: period, windows: map[string]*window{}, now: time.Now}
}

// Allow records a hit for key and reports whether it is within the limit. When
// rejected, retryAfter is the time until the window resets.
func (l *Limiter) Allow(key string) (allowed bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweep(now)

	w, ok := l.windows[key]
	if !ok || !now.Before(w.resetAt) {
		w = &window{resetAt: now.Add(l.period)}
		l.windows[key] = w
	}
	w.count++
	if w.count > l.limit {
		return false, w.resetAt.Sub(now)
	}
	return true, 0
}

func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < l.period {
		return
	}
	for k, w := range l.windows {
		if !now.Before(w.resetAt) {
			delete(l.windows, k)
		}
	}
	l.lastSweep = now
}

// PerIP limits requests by client IP within a named bucket.
func PerIP(name string, l *Limiter, responder *httpx.Responder) gin.HandlerFunc {
	return func(c *gin.Context) {
		allowed, retryAfter := l.Allow(name + ":" + c.ClientIP())
		if allowed {
			c.Next()
			return
		}
		seconds := int(retryAfter.Seconds())
		if retryAfter > time.Duration(seconds)*time.Second {
			seconds++
		}
		if seconds < 1 {
			seconds = 1
		}
		c.Header("Retry-After", strconv.Itoa(seconds))
		responder.Abort(c, apperrors.TooManyRequests("too many requests, retry in "+strconv.Itoa(seconds)+"s"))
	}
}

// Disabled is a pass-through middleware.
func Disabled() gin.HandlerFunc {
	return func(c *gin.Context) { c.Next() }
}
