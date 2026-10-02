package clock

import "time"

// Clock abstracts time for deterministic tests.
type Clock interface {
	Now() time.Time
}

// RealClock uses wall-clock time.
type RealClock struct{}

func (RealClock) Now() time.Time {
	return time.Now().UTC()
}

// MockClock is a fixed clock for tests.
type MockClock struct {
	Current time.Time
}

func (m *MockClock) Now() time.Time {
	return m.Current.UTC()
}

// Advance moves the mock clock forward.
func (m *MockClock) Advance(d time.Duration) {
	m.Current = m.Current.Add(d)
}
