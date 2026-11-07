package middleware

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/akqqqw/channelhunter/backend/internal/platform/apierror"
	"github.com/akqqqw/channelhunter/backend/internal/platform/httpx"
)

// RateLimiter is a token-bucket limiter keyed by client (IP or user ID).
//
// It is deliberately in-process: the MVP runs one API instance. When the API is
// scaled horizontally the same interface can be implemented on top of
// PostgreSQL or a shared cache without touching handlers.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64 // tokens per second
	burst   float64
	now     func() time.Time
}

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

// NewRateLimiter creates a limiter allowing `requests` per `window` with a burst
// equal to the same amount.
func NewRateLimiter(requests int, window time.Duration) *RateLimiter {
	if requests <= 0 || window <= 0 {
		requests, window = 60, time.Minute
	}
	return &RateLimiter{
		buckets: make(map[string]*bucket),
		rate:    float64(requests) / window.Seconds(),
		burst:   float64(requests),
		now:     time.Now,
	}
}

// Allow consumes a token for key. It returns the remaining tokens and whether
// the request may proceed.
func (l *RateLimiter) Allow(key string) (allow bool, remaining int, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, lastSeen: now}
		l.buckets[key] = b
	} else {
		elapsed := now.Sub(b.lastSeen).Seconds()
		if elapsed > 0 {
			b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rate)
			b.lastSeen = now
		}
	}

	if b.tokens < 1 {
		missing := 1 - b.tokens
		return false, 0, time.Duration(missing / l.rate * float64(time.Second))
	}

	b.tokens--
	return true, int(b.tokens), 0
}

// SetClock replaces the time source. Intended for tests that need to advance
// time deterministically.
func (l *RateLimiter) SetClock(now func() time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.now = now
}

// Cleanup removes buckets that have been idle for longer than ttl so the map
// cannot grow without bound.
func (l *RateLimiter) Cleanup(ttl time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := l.now().Add(-ttl)
	for key, b := range l.buckets {
		if b.lastSeen.Before(cutoff) {
			delete(l.buckets, key)
		}
	}
}

// Size reports the number of tracked clients (used by tests).
func (l *RateLimiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// RateLimit throttles requests per client key. keyFn decides the bucket; nil
// means "use the client IP".
func RateLimit(limiter *RateLimiter, trusted []*net.IPNet, keyFn func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := ""
			if keyFn != nil {
				key = keyFn(r)
			}
			if key == "" {
				key = ClientIP(r, trusted)
			}

			allow, remaining, retryAfter := limiter.Allow(key)
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			if !allow {
				seconds := int(math.Ceil(retryAfter.Seconds()))
				if seconds < 1 {
					seconds = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(seconds))
				httpx.Fail(w, r, apierror.New(apierror.CodeRateLimited,
					"Слишком много запросов. Попробуйте позже."))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
