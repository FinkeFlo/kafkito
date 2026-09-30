// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/auth"
	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

// noTestConnLimit is a Test connection rate limiter that does not limit in
// practice, for tests that send many Test connection requests to one
// server.
func noTestConnLimit() *rateLimiter {
	return newRateLimiter(1_000_000, time.Nanosecond, rateLimitMaxKeys, nil)
}

// fakeClock is a clock for rateLimiter that only moves on advance.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// requireBurst asserts that l lets exactly n requests of key through now.
func requireBurst(t *testing.T, l *rateLimiter, key string, n int) {
	t.Helper()
	for i := range n {
		ok, _ := l.allow(key)
		require.True(t, ok, "%s: request %d of %d", key, i+1, n)
	}
	ok, _ := l.allow(key)
	require.False(t, ok, "%s: request %d", key, n+1)
}

// A caller gets a burst of 10, then one request every 6 s, 10 per minute.
// A refused request learns the time until the next token.
func TestRateLimiter_BurstAndRefill(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newRateLimiter(testConnBurst, testConnInterval, rateLimitMaxKeys, clock.now)
	requireBurst(t, l, "k", 10)

	ok, wait := l.allow("k")
	require.False(t, ok)
	assert.Equal(t, 6*time.Second, wait)

	clock.advance(5500 * time.Millisecond)
	ok, wait = l.allow("k")
	require.False(t, ok)
	assert.Equal(t, 500*time.Millisecond, wait)

	clock.advance(500 * time.Millisecond)
	requireBurst(t, l, "k", 1)

	clock.advance(30 * time.Second)
	requireBurst(t, l, "k", 5)

	// A long pause refills the bucket to the burst, not beyond.
	clock.advance(10 * time.Minute)
	requireBurst(t, l, "k", 10)
}

// Each key has its own bucket.
func TestRateLimiter_KeysAreIndependent(t *testing.T) {
	t.Parallel()

	l := newRateLimiter(testConnBurst, testConnInterval, rateLimitMaxKeys, newFakeClock().now)
	requireBurst(t, l, "a", 10)
	requireBurst(t, l, "b", 10)
}

// A bucket that is full again holds nothing a new one would not, so the
// sweep, at most once a minute, drops it: idle callers take no memory.
func TestRateLimiter_DropsIdleBuckets(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newRateLimiter(testConnBurst, testConnInterval, rateLimitMaxKeys, clock.now)
	l.allow("idle")
	l.allow("busy")
	clock.advance(59 * time.Second)
	l.allow("busy")
	require.Len(t, l.fullAt, 2, "no sweep within a minute")

	clock.advance(2 * time.Second)
	l.allow("new")
	assert.ElementsMatch(t, []string{"busy", "new"}, slices.Collect(maps.Keys(l.fullAt)),
		"idle is full again and dropped, busy still refills")
	requireBurst(t, l, "idle", 10)
}

// With maxKeys buckets, a new key takes the place of a bucket that is full
// again or, when all are in use, of the one that is full again first.
func TestRateLimiter_CapsKeys(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newRateLimiter(testConnBurst, testConnInterval, 3, clock.now)
	for _, key := range []string{"a", "b", "c"} {
		l.allow(key)
		clock.advance(time.Second)
	}

	// All three are in use; a is full again first.
	l.allow("d")
	assert.ElementsMatch(t, []string{"b", "c", "d"}, slices.Collect(maps.Keys(l.fullAt)))

	// b is full again and makes room.
	clock.advance(4 * time.Second)
	l.allow("e")
	assert.ElementsMatch(t, []string{"c", "d", "e"}, slices.Collect(maps.Keys(l.fullAt)))

	// A known key takes no room.
	l.allow("c")
	assert.ElementsMatch(t, []string{"c", "d", "e"}, slices.Collect(maps.Keys(l.fullAt)))
}

func TestRetryAfterSeconds(t *testing.T) {
	t.Parallel()

	for d, want := range map[time.Duration]int{
		0:                       1,
		time.Nanosecond:         1,
		time.Second:             1,
		time.Second + 1:         2,
		5500 * time.Millisecond: 6,
		testConnInterval:        6,
	} {
		assert.Equal(t, want, retryAfterSeconds(d), "%v", d)
	}
}

// The caller is the verified principal, else the address of the
// connection. No request header changes it.
func TestRateLimitKey(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		remote    string
		principal *auth.Principal
		want      string
	}{
		{"user name", "192.0.2.1:1234", &auth.Principal{Subject: "sub-1", UserName: "alice"}, "user:alice"},
		{"subject without user name", "192.0.2.1:1234", &auth.Principal{Subject: "sub-1"}, "user:sub-1"},
		{"principal without either", "192.0.2.1:1234", &auth.Principal{}, "addr:192.0.2.1"},
		{"no principal", "192.0.2.1:1234", nil, "addr:192.0.2.1"},
		{"no principal, other port", "192.0.2.1:5678", nil, "addr:192.0.2.1"},
		{"IPv6", "[2001:db8::1]:443", nil, "addr:2001:db8::1"},
		{"address without port", "192.0.2.9", nil, "addr:192.0.2.9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/_test", nil)
			req.RemoteAddr = tc.remote
			if tc.principal != nil {
				req = req.WithContext(auth.WithPrincipal(req.Context(), tc.principal))
			}
			assert.Equal(t, tc.want, rateLimitKey(req))

			for _, h := range []string{config.DefaultIdentityHeader, rbacTestHeader, "X-Forwarded-For", "X-Real-Ip", "Forwarded"} {
				req.Header.Set(h, "198.51.100.7")
			}
			assert.Equal(t, tc.want, rateLimitKey(req), "request headers must not change the key")
		})
	}
}

// testConnServer serves New with Test connection and the topic list of a
// private cluster answered by fakes, and limiter (nil: the default).
func testConnServer(t *testing.T, cfg config.Config, limiter *rateLimiter, v auth.Validator) http.Handler {
	t.Helper()
	st := stores{
		clusters: fakeClusters{},
		topics:   fakeTopics{topics: []kafkapkg.TopicInfo{{Name: "orders"}}},
	}
	return New(Options{
		Version:         "v-test",
		Logger:          slog.New(slog.DiscardHandler),
		Config:          cfg,
		Auth:            v,
		stores:          &st,
		testConnLimiter: limiter,
	})
}

// postTestConn sends Test connection with testClusterBody from the address
// remote, with header.
func postTestConn(h http.Handler, remote string, header map[string]string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/_test", strings.NewReader(testClusterBody))
	req.RemoteAddr = remote
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return req, rec
}

// 40 Test connection requests of one caller to a fresh server: the burst of
// 10 passes, the rest get 429 rate_limited with Retry-After. A new RBAC
// identity header or X-Forwarded-For per request makes no new caller.
func TestTestConnection_RateLimited(t *testing.T) {
	t.Parallel()

	h := testConnServer(t, config.Defaults(), nil, nil)
	router := contractRouter(t)
	var passed, limited int
	for i := range 40 {
		req, rec := postTestConn(h, "192.0.2.1:1234", map[string]string{
			config.DefaultIdentityHeader: fmt.Sprintf("user-%d", i),
			"X-Forwarded-For":            fmt.Sprintf("198.51.100.%d", i),
		})
		switch rec.Code {
		case http.StatusOK:
			passed++
		case http.StatusTooManyRequests:
			limited++
			assert.JSONEq(t, `{"error":"too many requests","code":"rate_limited"}`, rec.Body.String())
			secs, err := strconv.Atoi(rec.Header().Get("Retry-After"))
			require.NoError(t, err)
			assert.GreaterOrEqual(t, secs, 1)
			assert.LessOrEqual(t, secs, 6)
			if limited == 1 {
				assertResponseMatchesSpec(t, router, req, rec)
			}
		default:
			require.Failf(t, "unexpected status", "request %d: %d %s", i+1, rec.Code, rec.Body.String())
		}
	}
	assert.Equal(t, 10, passed)
	assert.Equal(t, 30, limited)

	// Another address is another caller.
	_, rec := postTestConn(h, "192.0.2.2:1234", nil)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// Other routes are not limited.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+config.PrivateClusterSentinel+"/topics", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set(PrivateClusterHeader, encodeHeader(t, config.ClusterConfig{Brokers: []string{unreachableBroker}}))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// userNameValidator accepts any token as the principal of that name.
type userNameValidator struct{}

func (userNameValidator) Validate(_ context.Context, token string) (*auth.Principal, error) {
	return &auth.Principal{Subject: "sub-" + token, UserName: token}, nil
}

// With a verified principal, the principal is the caller, whatever address
// its requests come from.
func TestTestConnection_RateLimitedPerPrincipal(t *testing.T) {
	t.Parallel()

	h := testConnServer(t, config.Defaults(), nil, userNameValidator{})
	send := func(token, remote string) int {
		_, rec := postTestConn(h, remote, map[string]string{"Authorization": "Bearer " + token})
		return rec.Code
	}
	for i := range testConnBurst {
		require.Equal(t, http.StatusOK, send("alice", fmt.Sprintf("192.0.2.%d:1234", i+1)))
	}
	assert.Equal(t, http.StatusTooManyRequests, send("alice", "192.0.2.99:1234"))
	assert.Equal(t, http.StatusOK, send("bob", "192.0.2.1:1234"))
}

// A caller that private_clusters.mode refuses gets its 403 and uses no
// token: the limit applies after privateClusterGate.
func TestTestConnection_RateLimitAfterAccessCheck(t *testing.T) {
	t.Parallel()

	limiter := newTestConnLimiter()
	for _, tc := range privateAccessCases() {
		if tc.wantCode == "" {
			continue
		}
		h := testConnServer(t, tc.cfg, limiter, nil)
		for range 2 * testConnBurst {
			_, rec := postTestConn(h, "192.0.2.1:1234", map[string]string{rbacTestHeader: userMallory})
			require.Equal(t, http.StatusForbidden, rec.Code, "%s: %s", tc.name, rec.Body.String())
			assert.Contains(t, rec.Body.String(), `"code":"`+tc.wantCode+`"`)
		}
	}

	h := testConnServer(t, config.Defaults(), limiter, nil)
	for i := range testConnBurst {
		_, rec := postTestConn(h, "192.0.2.1:1234", nil)
		require.Equal(t, http.StatusOK, rec.Code, "request %d: %s", i+1, rec.Body.String())
	}
	_, rec := postTestConn(h, "192.0.2.1:1234", nil)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
}

// A request over the limit is answered before the X-Kafkito-Cluster header
// is decoded, so it resolves no host.
func TestTestConnection_RateLimitedRequestResolvesNoHost(t *testing.T) {
	t.Parallel()

	const host = "echo-broker.example.test"
	lookups := &countingLookup{unresolvable: []string{host}}
	st := stores{clusters: fakeClusters{}}
	h := New(Options{
		Version:    "v-test",
		Logger:     slog.New(slog.DiscardHandler),
		Config:     config.Defaults(),
		stores:     &st,
		lookupHost: lookups.lookup,
	})
	header := encodeHeader(t, config.ClusterConfig{Brokers: []string{host + ":9092"}})
	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/_test", nil)
		req.Header.Set(PrivateClusterHeader, header)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	for range testConnBurst {
		rec := send()
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{"error":"X-Kafkito-Cluster: broker 1: host name could not be resolved"}`, rec.Body.String())
	}
	require.Equal(t, map[string]int{host: testConnBurst}, lookups.counts())

	rec := send()
	require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
	assert.Equal(t, map[string]int{host: testConnBurst}, lookups.counts())
}
