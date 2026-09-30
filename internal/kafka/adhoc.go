// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/FinkeFlo/kafkito/internal/config"
	"github.com/FinkeFlo/kafkito/internal/masking"
	"github.com/twmb/franz-go/pkg/kgo"
)

// adhocIdleTTL is how long an unused ad-hoc cluster entry (and its kgo.Client)
// is kept before being evicted. Kept conservative: keeps hot tabs snappy but
// releases resources for closed ones.
const adhocIdleTTL = 15 * time.Minute

// adhocSweepInterval is how often the janitor evicts idle ad-hoc entries, so
// an entry outlives its last use by at most adhocIdleTTL plus this interval.
const adhocSweepInterval = time.Minute

// adhocFPKey returns the process-local secret used to key the ad-hoc
// fingerprint HMAC, generating it on first use. The key never leaves the
// process (not persisted, not logged, not part of any API response); its
// only purpose is to prevent Fingerprint's HMAC output from being reduced
// to an unkeyed hash of caller-supplied credentials.
func (r *Connections) adhocFPKey() []byte {
	r.adhocFPKeyOnce.Do(func() {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			// crypto/rand.Read failing indicates a broken OS entropy source;
			// this is effectively unreachable on any supported platform, but
			// fall back to a fixed key rather than panicking the registry —
			// worst case, ad-hoc client dedup degrades to per-process-boot
			// determinism instead of true randomness.
			copy(key, []byte("kafkito-adhoc-fingerprint-fallback-key-00000"))
		}
		r.adhocFPKeyVal = key
	})
	return r.adhocFPKeyVal
}

// Fingerprint returns a stable short digest identifying a cluster config's
// connection parameters, keyed by a process-local secret. Two configs with
// the same fingerprint reuse the same kgo.Client. The Name field is
// intentionally NOT part of the fingerprint — two identical configs with
// different display names still share a client.
//
// The digest is computed with HMAC-SHA256 rather than a bare hash: the
// input includes SASL/Schema-Registry passwords, and hashing sensitive
// credentials with an unkeyed fast hash is flagged as weak (CodeQL
// go/weak-sensitive-data-hashing) because the output could otherwise be
// brute-forced offline if it ever leaked (e.g. via logs). Keying the digest
// with a random, process-local secret (never persisted or transmitted)
// removes that risk while preserving the property this cache key actually
// needs: same input -> same output for the lifetime of the process.
func Fingerprint(cfg config.ClusterConfig, key []byte) string {
	h := hmac.New(sha256.New, key)
	brokers := append([]string{}, cfg.Brokers...)
	sort.Strings(brokers)
	// hmac.Hash.Write never errors; assign to _ to satisfy errcheck.
	_, _ = fmt.Fprintf(h, "brokers=%v\n", brokers)
	_, _ = fmt.Fprintf(h, "auth.type=%s\nauth.user=%s\nauth.pass=%s\n",
		cfg.Auth.Type, cfg.Auth.Username, cfg.Auth.Password)
	_, _ = fmt.Fprintf(h, "tls.enabled=%v\ntls.insecure=%v\n",
		cfg.TLS.Enabled, cfg.TLS.InsecureSkipVerify)
	_, _ = fmt.Fprintf(h, "sr.url=%s\nsr.user=%s\nsr.pass=%s\nsr.insecure=%v\n",
		cfg.SchemaRegistry.URL, cfg.SchemaRegistry.Username,
		cfg.SchemaRegistry.Password, cfg.SchemaRegistry.InsecureSkipVerify)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// UseAdhoc registers an ephemeral cluster configuration in the registry and
// returns the internal deterministic name to pass to all other Registry
// methods (ListTopics, DescribeTopic, ...). Safe to call concurrently and
// repeatedly for the same config — the existing registration is reused.
//
// The caller is expected to have validated cfg (non-empty brokers).
// The supplied cfg.Name is ignored; a fingerprint-derived internal name is
// used instead so unrelated users with identical connection parameters share
// the underlying kgo.Client (acceptable because they carry the same
// credentials anyway).
func (r *Connections) UseAdhoc(cfg config.ClusterConfig) (string, error) {
	if len(cfg.Brokers) == 0 {
		return "", errors.New("adhoc cluster: at least one broker required")
	}
	for _, b := range cfg.Brokers {
		if b == "" {
			return "", errors.New("adhoc cluster: empty broker address")
		}
	}
	fp := Fingerprint(cfg, r.adhocFPKey())
	name := config.AdhocClusterPrefix + fp

	r.mu.Lock()
	if _, exists := r.clusters[name]; exists {
		r.touchAdhocLocked(name)
		r.mu.Unlock()
		return name, nil
	}

	cfg.Name = name
	// Ad-hoc clusters never carry masking: the user brings their own creds
	// and sees raw data.
	empty, _ := masking.Compile(nil)

	r.clusters[name] = cfg
	r.masking[name] = empty
	if r.adhocLastUsed == nil {
		r.adhocLastUsed = make(map[string]time.Time, 4)
	}
	r.adhocLastUsed[name] = r.now()
	r.mu.Unlock()

	r.startJanitor()
	return name, nil
}

// touchAdhocLocked records a use of an ad-hoc cluster, which postpones its
// idle eviction. Names without a last-use entry (configured, evicted or
// unknown clusters) are left alone. Must be called while holding r.mu.
func (r *Connections) touchAdhocLocked(name string) {
	if _, ok := r.adhocLastUsed[name]; ok {
		r.adhocLastUsed[name] = r.now()
	}
}

// registered reports whether name is a registered cluster. Unlike ConfigFor
// it does not count as a use of an ad-hoc cluster.
func (r *Connections) registered(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.clusters[name]
	return ok
}

// evictIdleAdhoc removes every ad-hoc cluster whose last use is adhocIdleTTL
// or longer ago, together with everything kept for it: the client (closed),
// the config, the masking policy, the cached capabilities and Schema
// Registry decoder and, through evictHooks, the collected metrics and the
// cached topic configs. It is the only place that removes ad-hoc entries and
// returns how many are left.
func (r *Connections) evictIdleAdhoc() (remaining int) {
	cutoff := r.now().Add(-adhocIdleTTL)
	var evicted []string
	var clients []*kgo.Client

	r.mu.Lock()
	for name, last := range r.adhocLastUsed {
		if last.After(cutoff) {
			continue
		}
		evicted = append(evicted, name)
		if cl, ok := r.clients[name]; ok {
			clients = append(clients, cl)
			delete(r.clients, name)
		}
		delete(r.clusters, name)
		delete(r.masking, name)
		delete(r.adhocLastUsed, name)
		delete(r.caps, name)
	}
	remaining = len(r.adhocLastUsed)
	r.mu.Unlock()

	if len(evicted) == 0 {
		return remaining
	}
	r.srMu.Lock()
	for _, name := range evicted {
		delete(r.srDecoders, name)
	}
	r.srMu.Unlock()
	for _, drop := range r.evictHooks {
		drop(evicted)
	}
	// Closed without holding mu, so requests for other clusters do not wait
	// for the clients to shut down.
	for _, cl := range clients {
		cl.Close()
	}
	return remaining
}

// startJanitor arms the timer that runs evictIdleAdhoc every
// adhocSweepEvery, unless it is armed already or the registry is closed.
// UseAdhoc calls it after every new registration.
func (r *Connections) startJanitor() {
	r.janitorMu.Lock()
	defer r.janitorMu.Unlock()
	if r.janitor != nil || r.janitorClosed {
		return
	}
	r.janitor = time.AfterFunc(r.adhocSweepEvery, r.runJanitor)
}

// runJanitor evicts idle ad-hoc entries and re-arms the timer while any are
// left; once none is left the timer stays off until the next registration.
// It holds janitorMu for the whole run, so stopJanitor waits for a run in
// progress.
func (r *Connections) runJanitor() {
	r.janitorMu.Lock()
	defer r.janitorMu.Unlock()
	if r.janitorClosed {
		return
	}
	if r.evictIdleAdhoc() == 0 {
		r.janitor = nil
		return
	}
	r.janitor.Reset(r.adhocSweepEvery)
}

// stopJanitor stops the janitor for good: it waits for a run in progress
// and keeps later registrations from arming it again.
func (r *Connections) stopJanitor() {
	r.janitorMu.Lock()
	defer r.janitorMu.Unlock()
	r.janitorClosed = true
	if r.janitor != nil {
		r.janitor.Stop()
		r.janitor = nil
	}
}
