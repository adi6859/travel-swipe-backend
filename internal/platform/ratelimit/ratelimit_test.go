package ratelimit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLimiterWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := New(2, time.Minute)
	l.now = func() time.Time { return now }

	ok, _ := l.Allow("a")
	require.True(t, ok)
	ok, _ = l.Allow("a")
	require.True(t, ok)
	ok, retry := l.Allow("a")
	require.False(t, ok)
	require.Equal(t, time.Minute, retry)

	ok, _ = l.Allow("b")
	require.True(t, ok, "keys are independent")

	now = now.Add(time.Minute)
	ok, _ = l.Allow("a")
	require.True(t, ok, "window resets")
}

func TestLimiterSweepsExpiredWindows(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := New(1, time.Minute)
	l.now = func() time.Time { return now }

	for _, k := range []string{"a", "b", "c"} {
		l.Allow(k)
	}
	now = now.Add(2 * time.Minute)
	l.Allow("d")
	require.Len(t, l.windows, 1)
}
