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
