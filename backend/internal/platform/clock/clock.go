package clock

import "time"

// Clock interface allows for injecting time in business logic.
type Clock interface {
	Now() time.Time
}

// RealClock implements Clock using the real time package.
type RealClock struct{}

func (RealClock) Now() time.Time {
	return time.Now().UTC()
}

// FakeClock implements Clock for testing purposes.
type FakeClock struct {
	Time time.Time
}

func (fc FakeClock) Now() time.Time {
	return fc.Time
}
