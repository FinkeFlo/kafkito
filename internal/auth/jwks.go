// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwk"
)

const (
	// keyWaitTimeout bounds how long a caller waits for a running fetch.
	keyWaitTimeout = 5 * time.Second
	// keyFetchTimeout bounds a single JWKS fetch. It is longer than
	// keyWaitTimeout so a slow IdP still fills the cache for later requests.
	keyFetchTimeout = 10 * time.Second
)

var (
	// ErrKeysUnavailable reports that no key set could be loaded in time.
	ErrKeysUnavailable = errors.New("jwks: keys not available")
	// ErrKeySourceClosed reports a call on a KeySource after Close.
	ErrKeySourceClosed = errors.New("jwks: key source closed")
	// errEmptyKeySet rejects a JWKS document without keys.
	errEmptyKeySet = errors.New("jwks: empty key set")
)

// KeySource loads and caches the JSON Web Key Set served at one URL. All
// callers share one fetch at a time; a caller waits at most keyWaitTimeout
// (or until its context ends) and then fails instead of hanging. Close stops
// a running fetch. The zero value is not usable; call NewKeySource.
type KeySource struct {
	url         string
	client      jwk.HTTPClient
	waitTimeout time.Duration

	ctx    context.Context // lifetime of background fetches; Close cancels it
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	set      jwk.Set
	inflight chan struct{} // non-nil while a fetch runs; closed when it ends
	closed   bool
}

// NewKeySource returns a KeySource for the JWKS at url. It does no I/O; the
// first Keys call (or a validator's WarmUp) loads the set.
func NewKeySource(url string) *KeySource {
	ctx, cancel := context.WithCancel(context.Background())
	return &KeySource{
		url:         url,
		client:      jwk.DefaultHTTPClient(),
		waitTimeout: keyWaitTimeout,
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Keys returns the cached key set. Without one it starts a fetch (or joins the
// running one) and waits for it, bounded by ctx and keyWaitTimeout. kid, the
// key id from the token header, is not used yet.
func (s *KeySource) Keys(ctx context.Context, _ string) (jwk.Set, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrKeySourceClosed
	}
	if s.set != nil {
		set := s.set
		s.mu.Unlock()
		return set, nil
	}
	done := s.startFetchLocked()
	s.mu.Unlock()

	waitCtx, cancel := context.WithTimeout(ctx, s.waitTimeout)
	defer cancel()
	select {
	case <-done:
	case <-waitCtx.Done():
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrKeySourceClosed
	}
	if s.set == nil {
		return nil, ErrKeysUnavailable
	}
	return s.set, nil
}

// Close cancels a running fetch, waits for it to end and makes later Keys
// calls fail with ErrKeySourceClosed. Calling Close more than once is safe.
func (s *KeySource) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()

	s.cancel()
	s.wg.Wait()
}

// startFetchLocked starts a background fetch unless one is running and
// returns the channel that is closed when the fetch ends. s.mu must be held.
func (s *KeySource) startFetchLocked() <-chan struct{} {
	if s.inflight != nil {
		return s.inflight
	}
	done := make(chan struct{})
	s.inflight = done
	s.wg.Add(1)
	go s.fetch(done)
	return done
}

// fetch loads the set detached from any request context, so a canceled
// request does not abort the fetch other callers wait for.
func (s *KeySource) fetch(done chan struct{}) {
	defer s.wg.Done()

	ctx, cancel := context.WithTimeout(s.ctx, keyFetchTimeout)
	set, err := jwk.Fetch(ctx, s.url, jwk.WithHTTPClient(s.client))
	cancel()
	if err == nil && set.Len() == 0 {
		err = errEmptyKeySet
	}
	if err != nil && s.ctx.Err() == nil {
		slog.Warn("jwks fetch failed", "url", s.url, "err", err)
	}

	s.mu.Lock()
	if err == nil {
		s.set = set
	}
	s.inflight = nil
	s.mu.Unlock()
	close(done)
}
