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
	// keyRefreshInterval is the minimum time between two fetch attempts for
	// one URL, whatever triggered them (first load, unknown kid, stale set).
	keyRefreshInterval = time.Minute
	// keyMaxAge is how long a loaded set is used before a background refresh
	// starts. The old set stays in use until a refresh succeeds.
	keyMaxAge = 15 * time.Minute
)

var (
	// ErrKeysUnavailable reports that no key set could be loaded in time.
	ErrKeysUnavailable = errors.New("jwks: keys not available")
	// ErrKeySourceClosed reports a call on a KeySource after Close.
	ErrKeySourceClosed = errors.New("jwks: key source closed")
	// errEmptyKeySet rejects a JWKS document without keys.
	errEmptyKeySet = errors.New("jwks: empty key set")
)

// KeySource loads and caches the JSON Web Key Set served at one URL.
//
// All callers share one fetch at a time, and a new fetch starts at most once
// per keyRefreshInterval: to load the first set, when a token names a kid the
// cached set lacks, or in the background once the set is older than
// keyMaxAge. A caller waits at most keyWaitTimeout (or until its context
// ends) and then fails instead of hanging. Close stops a running fetch. The
// zero value is not usable; call NewKeySource.
type KeySource struct {
	url     string
	client  jwk.HTTPClient
	hideURL bool         // keep url out of logs; see WithURLHiddenInLogs
	logger  *slog.Logger // nil means slog.Default(); internal tests set it

	// The clock and the durations below are fixed by NewKeySource; internal
	// tests may replace them, but only before the first Keys call, because
	// they are read without holding mu.
	now             func() time.Time
	waitTimeout     time.Duration
	refreshInterval time.Duration
	maxAge          time.Duration

	ctx    context.Context // lifetime of background fetches; Close cancels it
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu          sync.Mutex
	set         jwk.Set
	fetchedAt   time.Time     // when set was loaded
	lastAttempt time.Time     // when the last fetch started; zero before the first
	inflight    chan struct{} // non-nil while a fetch runs; closed when it ends
	closed      bool
}

// KeySourceOption configures a KeySource.
type KeySourceOption func(*KeySource)

// WithURLHiddenInLogs keeps the URL, and error text that may carry it, out of
// the log line for a failed fetch. Use it when the URL comes from a token
// header rather than from configuration.
func WithURLHiddenInLogs() KeySourceOption {
	return func(s *KeySource) { s.hideURL = true }
}

// NewKeySource returns a KeySource for the JWKS at url. It does no I/O; the
// first Keys call (or a validator's WarmUp) loads the set.
func NewKeySource(url string, opts ...KeySourceOption) *KeySource {
	ctx, cancel := context.WithCancel(context.Background())
	s := &KeySource{
		url:             url,
		client:          jwk.DefaultHTTPClient(),
		now:             time.Now,
		waitTimeout:     keyWaitTimeout,
		refreshInterval: keyRefreshInterval,
		maxAge:          keyMaxAge,
		ctx:             ctx,
		cancel:          cancel,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Keys returns the key set to verify a token whose header names kid.
//
// If the cached set holds kid (or kid is empty) it is returned at once; a
// stale set additionally starts a background refresh. Otherwise Keys starts
// a fetch, unless one is running or the refresh interval has not passed, and
// waits for the running fetch, bounded by ctx and keyWaitTimeout. It then
// returns the newest set even if that still lacks kid, so signature
// verification decides. Without any set it returns ErrKeysUnavailable.
func (s *KeySource) Keys(ctx context.Context, kid string) (jwk.Set, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrKeySourceClosed
	}
	if s.set != nil && (kid == "" || hasKeyID(s.set, kid)) {
		set := s.set
		if s.now().Sub(s.fetchedAt) >= s.maxAge {
			s.startFetchLocked()
		}
		s.mu.Unlock()
		return set, nil
	}
	done := s.startFetchLocked()
	s.mu.Unlock()

	if done != nil {
		waitCtx, cancel := context.WithTimeout(ctx, s.waitTimeout)
		defer cancel()
		select {
		case <-done:
		case <-waitCtx.Done():
		}
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

// startFetchLocked returns the channel of the running fetch, or starts a new
// fetch if the refresh interval allows it. It returns nil when no fetch runs
// and none may start. s.mu must be held.
func (s *KeySource) startFetchLocked() <-chan struct{} {
	if s.inflight != nil {
		return s.inflight
	}
	now := s.now()
	if !s.lastAttempt.IsZero() && now.Sub(s.lastAttempt) < s.refreshInterval {
		return nil
	}
	s.lastAttempt = now
	done := make(chan struct{})
	s.inflight = done
	s.wg.Add(1)
	go s.fetch(done)
	return done
}

// fetch loads the set detached from any request context, so a canceled
// request does not abort the fetch other callers wait for. A failed fetch
// keeps the previous set.
func (s *KeySource) fetch(done chan struct{}) {
	defer s.wg.Done()

	ctx, cancel := context.WithTimeout(s.ctx, keyFetchTimeout)
	set, err := jwk.Fetch(ctx, s.url, jwk.WithHTTPClient(s.client))
	cancel()
	if err == nil && set.Len() == 0 {
		err = errEmptyKeySet
	}
	if err != nil && s.ctx.Err() == nil {
		s.logFetchFailure(err)
	}

	s.mu.Lock()
	if err == nil {
		s.set = set
		s.fetchedAt = s.now()
	}
	s.inflight = nil
	s.mu.Unlock()
	close(done)
}

// logFetchFailure logs a failed fetch at WARN. With hideURL the jwx error is
// replaced by a coarse cause, because its text names the URL.
func (s *KeySource) logFetchFailure(err error) {
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	if !s.hideURL {
		logger.Warn("jwks fetch failed", "url", s.url, "err", err)
		return
	}
	cause := "request or parse failed"
	switch {
	case errors.Is(err, errEmptyKeySet):
		cause = errEmptyKeySet.Error()
	case errors.Is(err, context.DeadlineExceeded):
		cause = "timeout"
	}
	logger.Warn("jwks fetch failed", "url", "(token jku, not logged)", "err", cause)
}

func hasKeyID(set jwk.Set, kid string) bool {
	_, ok := set.LookupKeyID(kid)
	return ok
}
