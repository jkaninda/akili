// Package ratelimit implements a per-user token bucket rate limiter.
// Thread-safe. No background goroutines — tokens are refilled lazily on each Allow call.
package ratelimit

import (
	"errors"
	"sync"
	"time"
)

// ErrRateLimited is returned when a user has exhausted their token bucket.
var ErrRateLimited = errors.New("rate limit exceeded")

// Config configures the token bucket rate limiter.
type Config struct {
	RequestsPerMinute int // Tokens added per minute. 0 = unlimited (Allow always succeeds).
	BurstSize         int // Maximum tokens in bucket. 0 = defaults to RequestsPerMinute.
}

// Limiter is a per-user token bucket rate limiter.
// Each user gets an independent bucket; one user cannot exhaust another's quota.
type Limiter struct {
	mu    sync.Mutex
	users map[string]*bucket
	rate  float64 // tokens per second
	burst float64 // max bucket capacity
}

type bucket struct {
	tokens   float64
	lastFill time.Time
}

// NewLimiter creates a rate limiter with the given configuration.
// If RequestsPerMinute is 0, Allow always succeeds (unlimited).
func NewLimiter(cfg Config) *Limiter {
	burst := cfg.BurstSize
	if burst <= 0 {
		burst = cfg.RequestsPerMinute
	}
	if burst <= 0 {
		burst = 1 // safety floor
	}
	return &Limiter{
		users: make(map[string]*bucket),
		rate:  float64(cfg.RequestsPerMinute) / 60.0,
		burst: float64(burst),
	}
}

// Allow checks whether the user has tokens remaining.
// Consumes one token on success. Returns ErrRateLimited if the bucket is empty.
func (l *Limiter) Allow(userID string) error {
	// Unlimited mode.
	if l.rate <= 0 {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.users[userID]
	if !ok {
		// First request: start with a full bucket.
		b = &bucket{tokens: l.burst, lastFill: now}
		l.users[userID] = b
	}

	// Refill tokens based on elapsed time.
	elapsed := now.Sub(b.lastFill).Seconds()
	b.tokens += elapsed * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.lastFill = now

	// Try to consume one token.
	if b.tokens < 1 {
		return ErrRateLimited
	}
	b.tokens--
	return nil
}
