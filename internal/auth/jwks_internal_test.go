// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jwksServer is a JWKS endpoint whose answer a test can change: it serves a
// set with the configured key ids, a failure status, or hangs until the
// request is canceled.
type jwksServer struct {
	srv  *httptest.Server
	hits atomic.Int32

	mu     sync.Mutex
	body   []byte
	status int
	hang   bool
}

func newJWKSServer(t *testing.T, kids ...string) *jwksServer {
	t.Helper()

	js := &jwksServer{}
	js.serveKeys(t, kids...)
	js.srv = httptest.NewServer(http.HandlerFunc(js.handle))
	t.Cleanup(js.srv.Close)
	return js
}

func (js *jwksServer) handle(w http.ResponseWriter, r *http.Request) {
	js.hits.Add(1)
	js.mu.Lock()
	body, status, hang := js.body, js.status, js.hang
	js.mu.Unlock()

	if hang {
		<-r.Context().Done()
		return
	}
	if status != http.StatusOK {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// serveKeys makes the server answer with a set holding one EC key per kid.
func (js *jwksServer) serveKeys(t *testing.T, kids ...string) {
	t.Helper()

	set := jwk.NewSet()
	for _, kid := range kids {
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		key, err := jwk.Import(&priv.PublicKey)
		require.NoError(t, err)
		require.NoError(t, key.Set(jwk.KeyIDKey, kid))
		require.NoError(t, set.AddKey(key))
	}
	body, err := json.Marshal(set)
	require.NoError(t, err)

	js.mu.Lock()
	defer js.mu.Unlock()
	js.body, js.status, js.hang = body, http.StatusOK, false
}

func (js *jwksServer) fail(status int) {
	js.mu.Lock()
	defer js.mu.Unlock()
	js.status = status
}

func (js *jwksServer) hangRequests() {
	js.mu.Lock()
	defer js.mu.Unlock()
	js.hang = true
}

func (js *jwksServer) url() string { return js.srv.URL + "/jwks" }

func newTestKeySource(t *testing.T, url string) *KeySource {
	t.Helper()

	s := NewKeySource(url)
	t.Cleanup(s.Close)
	return s
}

func TestKeySource_ConcurrentFirstCalls_ShareOneFetch(t *testing.T) {
	t.Parallel()

	js := newJWKSServer(t, "k1")
	s := newTestKeySource(t, js.url())

	const callers = 32
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			<-start
			set, err := s.Keys(context.Background(), "k1")
			if err == nil && set.Len() != 1 {
				err = assert.AnError
			}
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, int32(1), js.hits.Load(), "concurrent first calls must share one fetch")
}

func TestKeySource_BoundsTheWaitWhenTheIdPHangs(t *testing.T) {
	t.Parallel()

	js := newJWKSServer(t, "k1")
	js.hangRequests()
	s := newTestKeySource(t, js.url())
	s.waitTimeout = 50 * time.Millisecond

	begin := time.Now()
	_, err := s.Keys(context.Background(), "k1")

	require.ErrorIs(t, err, ErrKeysUnavailable)
	assert.Less(t, time.Since(begin), 2*time.Second, "Keys must give up after the wait timeout")
}

func TestKeySource_StopsWaitingWhenTheCallerContextEnds(t *testing.T) {
	t.Parallel()

	js := newJWKSServer(t, "k1")
	js.hangRequests()
	s := newTestKeySource(t, js.url())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	begin := time.Now()
	_, err := s.Keys(ctx, "k1")

	require.ErrorIs(t, err, ErrKeysUnavailable)
	assert.Less(t, time.Since(begin), 2*time.Second, "Keys must honor the caller's deadline")
}

func TestKeySource_FailedFetch_ReturnsErrKeysUnavailable(t *testing.T) {
	t.Parallel()

	js := newJWKSServer(t, "k1")
	js.fail(http.StatusInternalServerError)
	s := newTestKeySource(t, js.url())

	_, err := s.Keys(context.Background(), "k1")

	require.ErrorIs(t, err, ErrKeysUnavailable)
	assert.NotContains(t, err.Error(), js.srv.URL, "the error must not carry the URL")
}

func TestKeySource_EmptySet_CountsAsFailure(t *testing.T) {
	t.Parallel()

	js := newJWKSServer(t) // no kids: {"keys":[]}
	s := newTestKeySource(t, js.url())

	_, err := s.Keys(context.Background(), "")

	require.ErrorIs(t, err, ErrKeysUnavailable)
}

func TestKeySource_Close_CancelsAHangingFetchAndRejectsLaterCalls(t *testing.T) {
	t.Parallel()

	js := newJWKSServer(t, "k1")
	js.hangRequests()
	s := NewKeySource(js.url())
	t.Cleanup(s.Close) // a failed assertion must not leave the fetch hanging
	s.waitTimeout = 10 * time.Millisecond
	_, err := s.Keys(context.Background(), "k1") // leaves the fetch hanging
	require.ErrorIs(t, err, ErrKeysUnavailable)

	closed := make(chan struct{})
	go func() {
		s.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close must cancel the running fetch and return")
	}

	_, err = s.Keys(context.Background(), "k1")
	require.ErrorIs(t, err, ErrKeySourceClosed)
	s.Close() // second Close is a no-op
}

// fakeClock is a manually advanced clock for the refresh interval and the
// max age; waits still use real (short) timeouts.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newClockedKeySource(t *testing.T, url string) (*KeySource, *fakeClock) {
	t.Helper()

	clk := newFakeClock()
	s := newTestKeySource(t, url)
	s.now = clk.Now
	return s, clk
}

func TestKeySource_KnownKid_DoesNotFetchAgain(t *testing.T) {
	t.Parallel()

	js := newJWKSServer(t, "k1")
	s, clk := newClockedKeySource(t, js.url())
	_, err := s.Keys(context.Background(), "k1")
	require.NoError(t, err)
	clk.Advance(10 * time.Minute)

	_, err = s.Keys(context.Background(), "k1")
	require.NoError(t, err)
	_, err = s.Keys(context.Background(), "")
	require.NoError(t, err)

	assert.Equal(t, int32(1), js.hits.Load(), "a fresh set that holds the kid needs no fetch")
}

func TestKeySource_UnknownKid_RefreshesAtMostOncePerInterval(t *testing.T) {
	t.Parallel()

	js := newJWKSServer(t, "k1")
	s, clk := newClockedKeySource(t, js.url())
	_, err := s.Keys(context.Background(), "k1")
	require.NoError(t, err)
	js.serveKeys(t, "k2") // the IdP rotates its key

	// Within the interval the old set comes back without a fetch.
	clk.Advance(59 * time.Second)
	set, err := s.Keys(context.Background(), "k2")
	require.NoError(t, err)
	assert.False(t, hasKeyID(set, "k2"), "no refresh before the interval has passed")
	assert.Equal(t, int32(1), js.hits.Load())

	// After the interval one refresh loads the rotated key.
	clk.Advance(2 * time.Second)
	set, err = s.Keys(context.Background(), "k2")
	require.NoError(t, err)
	assert.True(t, hasKeyID(set, "k2"), "the refresh must load the rotated key")
	assert.Equal(t, int32(2), js.hits.Load())
}

func TestKeySource_UnknownKidFlood_SharesOneRefresh(t *testing.T) {
	t.Parallel()

	js := newJWKSServer(t, "k1")
	s, clk := newClockedKeySource(t, js.url())
	_, err := s.Keys(context.Background(), "k1")
	require.NoError(t, err)
	clk.Advance(time.Minute)

	const callers = 50
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			// Every caller names a kid the IdP never serves.
			_, _ = s.Keys(context.Background(), "unknown")
		})
	}
	wg.Wait()

	assert.Equal(t, int32(2), js.hits.Load(), "a flood of unknown kids must cause exactly one refresh")
}

func TestKeySource_RecoversAfterAFailedFetch(t *testing.T) {
	t.Parallel()

	js := newJWKSServer(t, "k1")
	js.fail(http.StatusServiceUnavailable)
	s, clk := newClockedKeySource(t, js.url())
	_, err := s.Keys(context.Background(), "k1")
	require.ErrorIs(t, err, ErrKeysUnavailable)
	js.serveKeys(t, "k1") // the IdP is back

	// Within the interval the failure is not retried, and Keys does not wait.
	_, err = s.Keys(context.Background(), "k1")
	require.ErrorIs(t, err, ErrKeysUnavailable)
	assert.Equal(t, int32(1), js.hits.Load())

	clk.Advance(time.Minute)
	_, err = s.Keys(context.Background(), "k1")
	require.NoError(t, err, "the next attempt after the interval must succeed")
	assert.Equal(t, int32(2), js.hits.Load())
}

func TestKeySource_StaleSet_RefreshesInTheBackground(t *testing.T) {
	t.Parallel()

	js := newJWKSServer(t, "k1")
	s, clk := newClockedKeySource(t, js.url())
	_, err := s.Keys(context.Background(), "k1")
	require.NoError(t, err)
	js.serveKeys(t, "k1", "k2")
	clk.Advance(15 * time.Minute)

	set, err := s.Keys(context.Background(), "k1")

	require.NoError(t, err)
	assert.False(t, hasKeyID(set, "k2"), "the stale set is returned without waiting")
	require.Eventually(t, func() bool {
		set, err := s.Keys(context.Background(), "k2")
		return err == nil && hasKeyID(set, "k2")
	}, 2*time.Second, 5*time.Millisecond, "the background refresh must load the new set")
	assert.Equal(t, int32(2), js.hits.Load())
}

func TestKeySource_FailedRefresh_KeepsThePreviousSet(t *testing.T) {
	t.Parallel()

	js := newJWKSServer(t, "k1")
	s, clk := newClockedKeySource(t, js.url())
	_, err := s.Keys(context.Background(), "k1")
	require.NoError(t, err)
	js.fail(http.StatusInternalServerError)
	clk.Advance(time.Minute)

	set, err := s.Keys(context.Background(), "k2") // unknown kid: refresh fails

	require.NoError(t, err, "a failed refresh must not drop the cached set")
	assert.True(t, hasKeyID(set, "k1"))
	assert.Equal(t, int32(2), js.hits.Load())
}
