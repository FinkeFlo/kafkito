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

	"github.com/FinkeFlo/kafkito/pkg/config"
	"github.com/FinkeFlo/kafkito/pkg/masking"
)

// AdhocPrefix is the internal cluster-name prefix used for ephemeral clusters
// that originate from a per-request X-Kafkito-Cluster header (private clusters
// stored in the user's browser). Shared cluster names never collide because
// config.Load rejects empty names, and this prefix is reserved.
const AdhocPrefix = "__adhoc_"

// adhocIdleTTL is how long an unused ad-hoc cluster entry (and its kgo.Client)
// is kept before being evicted. Kept conservative: keeps hot tabs snappy but
// releases resources for closed ones.
const adhocIdleTTL = 15 * time.Minute

// IsAdhoc reports whether a cluster name belongs to an ad-hoc (private)
// cluster registered via UseAdhoc.
func IsAdhoc(name string) bool {
	return len(name) > len(AdhocPrefix) && name[:len(AdhocPrefix)] == AdhocPrefix
}

// adhocFPKey returns the process-local secret used to key the ad-hoc
// fingerprint HMAC, generating it on first use. The key never leaves the
// process (not persisted, not logged, not part of any API response); its
// only purpose is to prevent Fingerprint's HMAC output from being reduced
// to an unkeyed hash of caller-supplied credentials.
func (r *Registry) adhocFPKey() []byte {
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
func (r *Registry) UseAdhoc(cfg config.ClusterConfig) (string, error) {
	if len(cfg.Brokers) == 0 {
		return "", errors.New("adhoc cluster: at least one broker required")
	}
	for _, b := range cfg.Brokers {
		if b == "" {
			return "", errors.New("adhoc cluster: empty broker address")
		}
	}
	fp := Fingerprint(cfg, r.adhocFPKey())
	name := AdhocPrefix + fp

	r.mu.Lock()
	defer r.mu.Unlock()

	// Opportunistic sweep of idle adhoc entries to bound memory.
	r.sweepAdhocLocked()

	if _, exists := r.clusters[name]; exists {
		r.touchAdhocLocked(name)
		return name, nil
	}

	cfg.Name = name
	// Ad-hoc clusters never carry masking: the user brings their own creds
	// and sees raw data.
	empty, _ := masking.Compile(nil)

	r.clusters[name] = cfg
	r.masking[name] = empty
	r.touchAdhocLocked(name)
	return name, nil
}

// touchAdhocLocked records the last-use timestamp for an adhoc cluster.
// Must be called while holding r.mu.
func (r *Registry) touchAdhocLocked(name string) {
	if r.adhocLastUsed == nil {
		r.adhocLastUsed = make(map[string]time.Time, 4)
	}
	r.adhocLastUsed[name] = time.Now()
}

// sweepAdhocLocked evicts adhoc entries whose last use is older than
// adhocIdleTTL. Must be called while holding r.mu.
func (r *Registry) sweepAdhocLocked() {
	if len(r.adhocLastUsed) == 0 {
		return
	}
	cutoff := time.Now().Add(-adhocIdleTTL)
	for name, last := range r.adhocLastUsed {
		if last.After(cutoff) {
			continue
		}
		if cl, ok := r.clients[name]; ok {
			cl.Close()
			delete(r.clients, name)
		}
		delete(r.clusters, name)
		delete(r.masking, name)
		delete(r.adhocLastUsed, name)
		capCaches.Delete(name)

		r.srMu.Lock()
		delete(r.srDecoders, name)
		r.srMu.Unlock()
	}
}
