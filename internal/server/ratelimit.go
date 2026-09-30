// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

// Test connection resolves and dials the hosts of the definition it gets, so
// each caller may send testConnBurst requests at once and then one per
// testConnInterval: 10 per minute.
const (
	testConnBurst    = 10
	testConnInterval = 6 * time.Second
)

const (
	// rateLimitMaxKeys caps the callers a rateLimiter keeps a bucket for.
	rateLimitMaxKeys = 10_000
	// rateLimitSweepEvery is how often a rateLimiter drops the buckets that
	// are full again.
	rateLimitSweepEvery = time.Minute
	// rateLimitedCode is the code of the 429 of a rate-limited request.
	rateLimitedCode = "rate_limited"
)

// rateLimiter is a token bucket per key: burst tokens, refilled by one per
// interval. It keeps a bucket as the time it is full again (GCRA), so a
// bucket whose time has passed holds nothing a new bucket would not: sweep
// drops those, which bounds the memory of idle callers, and with maxKeys
// buckets the fullest one makes room for a new key. It is safe for
// concurrent use.
type rateLimiter struct {
	burst    int
	interval time.Duration
	maxKeys  int
	now      func() time.Time

	mu        sync.Mutex
	fullAt    map[string]time.Time
	lastSweep time.Time
}

// newRateLimiter returns a rateLimiter; now is its clock (nil: time.Now).
func newRateLimiter(burst int, interval time.Duration, maxKeys int, now func() time.Time) *rateLimiter {
	if now == nil {
		now = time.Now
	}
	return &rateLimiter{
		burst:    burst,
		interval: interval,
		maxKeys:  maxKeys,
		now:      now,
		fullAt:   make(map[string]time.Time),
	}
}

// newTestConnLimiter returns the rate limiter of Test connection.
func newTestConnLimiter() *rateLimiter {
	return newRateLimiter(testConnBurst, testConnInterval, rateLimitMaxKeys, nil)
}

// allow takes a token from the bucket of key. Without one it returns false
// and the time until the next token.
func (l *rateLimiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.lastSweep) >= rateLimitSweepEvery {
		l.sweep(now)
	}
	fullAt, known := l.fullAt[key]
	if !known || fullAt.Before(now) {
		fullAt = now
	}
	next := fullAt.Add(l.interval)
	if wait := next.Sub(now) - time.Duration(l.burst)*l.interval; wait > 0 {
		return false, wait
	}
	if !known && len(l.fullAt) >= l.maxKeys {
		l.makeRoom(now)
	}
	l.fullAt[key] = next
	return true, 0
}

// sweep drops the buckets that are full again.
func (l *rateLimiter) sweep(now time.Time) {
	for key, fullAt := range l.fullAt {
		if !fullAt.After(now) {
			delete(l.fullAt, key)
		}
	}
	l.lastSweep = now
}

// makeRoom frees the slot of one bucket: it sweeps and, when every bucket
// is still in use, drops the one that is full again first.
func (l *rateLimiter) makeRoom(now time.Time) {
	l.sweep(now)
	if len(l.fullAt) < l.maxKeys {
		return
	}
	var (
		oldest   string
		oldestAt time.Time
		found    bool
	)
	for key, fullAt := range l.fullAt {
		if !found || fullAt.Before(oldestAt) {
			oldest, oldestAt, found = key, fullAt, true
		}
	}
	delete(l.fullAt, oldest)
}

// retryAfterSeconds is d in whole seconds, rounded up, and at least 1.
func retryAfterSeconds(d time.Duration) int {
	return max(int((d+time.Second-1)/time.Second), 1)
}

// rateLimitKey is the caller a rate limit counts: the verified principal
// (user name, else subject), else the host of the connection's remote
// address. It never reads a request header. Without a principal, the RBAC
// identity header (see rbacSubject) and X-Forwarded-For are chosen by the
// client, which could send a new value with every request.
func rateLimitKey(r *http.Request) string {
	if p, ok := auth.PrincipalFromContext(r.Context()); ok && p != nil {
		if p.UserName != "" {
			return "user:" + p.UserName
		}
		if p.Subject != "" {
			return "user:" + p.Subject
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "addr:" + host
}

// testConnRateLimit applies limiter to Test connection (testClusterRoute)
// and answers 429 rate_limited with Retry-After when the caller has no
// token left. It runs after privateClusterGate, so a caller that
// private_clusters.mode refuses gets its 403 without using a token, and
// before the X-Kafkito-Cluster header is decoded, so a refused request
// resolves no host.
func testConnRateLimit(limiter *rateLimiter, errs errorWriter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method+" "+routePattern(r) == testClusterRoute {
				if ok, wait := limiter.allow(rateLimitKey(r)); !ok {
					w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(wait)))
					errs.writeError(w, r, &apiError{
						Status:  http.StatusTooManyRequests,
						Code:    rateLimitedCode,
						Message: "too many requests",
					})
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
