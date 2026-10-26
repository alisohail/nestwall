package auth

import (
	"errors"
	"sync"
	"time"

	"nestwall/backend/internal/platform/clock"
)

// Rate limit settings for login attempts.
const (
	maxLoginAttemptsPerIP   = 10
	maxLoginAttemptsPerUser = 5
	loginAttemptWindow      = 15 * time.Minute
)

// ErrRateLimited is returned when rate limit is exceeded.
var ErrRateLimited = errors.New("rate limited")

// RateLimiter limits failed login attempts per client IP and per user ID.
type RateLimiter struct {
	mu       sync.Mutex
	clock    clock.Clock
	attempts map[string][]time.Time
}

// NewRateLimiter creates a rate limiter that reads time from clk.
func NewRateLimiter(clk clock.Clock) *RateLimiter {
	return &RateLimiter{
		clock:    clk,
		attempts: make(map[string][]time.Time),
	}
}

// CheckRateLimit returns ErrRateLimited if the IP or user has reached its limit
// in the current window. It does not record anything; call RecordFailure after a failed login.
func (rl *RateLimiter) CheckRateLimit(ip, userID string) error {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.clock.Now()
	if len(rl.recent(ip, now)) >= maxLoginAttemptsPerIP {
		return ErrRateLimited
	}
	if userID != "" && len(rl.recent(userKey(userID), now)) >= maxLoginAttemptsPerUser {
		return ErrRateLimited
	}
	return nil
}

// RecordFailure records one failed login attempt for the IP and the user ID.
func (rl *RateLimiter) RecordFailure(ip, userID string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.clock.Now()
	rl.attempts[ip] = append(rl.recent(ip, now), now)
	if userID != "" {
		key := userKey(userID)
		rl.attempts[key] = append(rl.recent(key, now), now)
	}
}

// ClearUser clears the attempt history for a user after a successful login.
func (rl *RateLimiter) ClearUser(userID string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.attempts, userKey(userID))
}

// recent returns the attempts for key that are inside the window and drops the rest.
// The caller must hold rl.mu.
func (rl *RateLimiter) recent(key string, now time.Time) []time.Time {
	return rl.recentWithin(key, now, loginAttemptWindow)
}

// recentWithin is recent with an explicit window length.
func (rl *RateLimiter) recentWithin(key string, now time.Time, window time.Duration) []time.Time {
	cutoff := now.Add(-window)
	var kept []time.Time
	for _, t := range rl.attempts[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(rl.attempts, key)
	} else {
		rl.attempts[key] = kept
	}
	return kept
}

// CheckWindow returns ErrRateLimited when key has reached max actions in the
// window. Use it for other actions such as forgot-password requests. It does
// not record anything; call RecordWindow after the action.
func (rl *RateLimiter) CheckWindow(key string, max int, window time.Duration) error {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if len(rl.recentWithin(key, rl.clock.Now(), window)) >= max {
		return ErrRateLimited
	}
	return nil
}

// RecordWindow records one action for key.
func (rl *RateLimiter) RecordWindow(key string, window time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.clock.Now()
	rl.attempts[key] = append(rl.recentWithin(key, now, window), now)
}

func userKey(userID string) string {
	return "user:" + userID
}
