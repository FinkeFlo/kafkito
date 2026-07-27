// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

// adhoc_test.go covers the HMAC-keyed Fingerprint used for ad-hoc cluster
// client dedup (CodeQL go/weak-sensitive-data-hashing remediation): the
// digest must depend on the config, stay stable across calls with the same
// key, and change if the key changes, so a bare-hash regression would be
// caught here.

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/pkg/config"
)

func sampleClusterConfig() config.ClusterConfig {
	return config.ClusterConfig{
		Name:    "irrelevant-to-fingerprint",
		Brokers: []string{"b2:9092", "b1:9092"},
		Auth: config.AuthConfig{
			Type:     "sasl-plain",
			Username: "alice",
			Password: "s3cr3t",
		},
	}
}

func TestFingerprint_StableForSameConfigAndKey(t *testing.T) {
	t.Parallel()

	key := []byte("fixed-test-key-0123456789012345")
	cfg := sampleClusterConfig()

	got1 := Fingerprint(cfg, key)
	got2 := Fingerprint(cfg, key)

	assert.Equal(t, got1, got2, "same config + same key must yield the same fingerprint")
	assert.NotEmpty(t, got1)
}

func TestFingerprint_IgnoresBrokerOrderAndName(t *testing.T) {
	t.Parallel()

	key := []byte("fixed-test-key-0123456789012345")
	cfg := sampleClusterConfig()
	reordered := cfg
	reordered.Name = "totally-different-display-name"
	reordered.Brokers = []string{"b1:9092", "b2:9092"}

	assert.Equal(t, Fingerprint(cfg, key), Fingerprint(reordered, key),
		"broker order and Name must not affect the fingerprint")
}

func TestFingerprint_DiffersForDifferentConfig(t *testing.T) {
	t.Parallel()

	key := []byte("fixed-test-key-0123456789012345")
	cfg := sampleClusterConfig()
	other := cfg
	other.Auth.Password = "different-password"

	assert.NotEqual(t, Fingerprint(cfg, key), Fingerprint(other, key),
		"a different password must produce a different fingerprint")
}

func TestFingerprint_DiffersForDifferentKey(t *testing.T) {
	t.Parallel()

	cfg := sampleClusterConfig()
	keyA := []byte("key-a-0123456789012345678901234")
	keyB := []byte("key-b-0123456789012345678901234")

	assert.NotEqual(t, Fingerprint(cfg, keyA), Fingerprint(cfg, keyB),
		"the digest must be keyed: changing the key must change the output "+
			"even for an identical config (this is what distinguishes the fix "+
			"from a bare unkeyed hash)")
}

func TestRegistry_AdhocFPKey_LazyStableAndRandomPerRegistry(t *testing.T) {
	t.Parallel()

	r1 := NewRegistry(nil, slog.Default())
	r2 := NewRegistry(nil, slog.Default())

	k1a := r1.adhocFPKey()
	k1b := r1.adhocFPKey()
	assert.Equal(t, k1a, k1b, "adhocFPKey must be stable across calls on the same registry")
	require.Len(t, k1a, 32)

	k2 := r2.adhocFPKey()
	assert.NotEqual(t, k1a, k2, "each registry must get its own random key")
}

func TestRegistry_UseAdhoc_DedupsIdenticalConfigsViaFingerprint(t *testing.T) {
	t.Parallel()

	r := NewRegistry(nil, slog.Default())
	cfg := config.ClusterConfig{Name: "n1", Brokers: []string{"b1:9092"}}

	name1, err := r.UseAdhoc(cfg)
	require.NoError(t, err)
	name2, err := r.UseAdhoc(cfg)
	require.NoError(t, err)

	assert.Equal(t, name1, name2, "identical configs must dedup to the same internal name")
	assert.True(t, IsAdhoc(name1))

	diff := cfg
	diff.Brokers = []string{"b2:9092"}
	name3, err := r.UseAdhoc(diff)
	require.NoError(t, err)
	assert.NotEqual(t, name1, name3, "a different config must get a different internal name")
}
