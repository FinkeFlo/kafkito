// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

// registry_ssrf_test.go contains focused unit tests for the SSRF-hardening
// changes in clientOpts (finding #4, MEDIUM): ad-hoc clusters receive a
// GuardedDialContext dialer; operator-configured clusters do not.

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/pkg/config"
	"github.com/FinkeFlo/kafkito/pkg/netguard"
	"github.com/twmb/franz-go/pkg/kgo"
)

// brokerAddr is a loopback address that is (a) not reachable (no listener) and
// (b) blocked by GuardedDialContext. Using 169.254.169.254 (metadata endpoint)
// avoids any race with local services that happen to listen on port 9092.
const blockedBroker = "169.254.169.254:9092"

// adhocClusterName returns a cluster name that passes IsAdhoc.
func adhocName() string { return AdhocPrefix + "test-cluster" }

// configuredName returns a cluster name that does NOT pass IsAdhoc.
func configuredName() string { return "my-configured-cluster" }

// TestClientOpts_AdhocCluster_DialBlockedAddress verifies that an ad-hoc kgo
// client refuses to connect to a blocked address (cloud metadata endpoint) at
// dial time, returning netguard.ErrBlockedAddress.
func TestClientOpts_AdhocCluster_DialBlockedAddress(t *testing.T) {
	t.Parallel()

	cfg := config.ClusterConfig{
		Name:    adhocName(),
		Brokers: []string{blockedBroker},
	}
	opts := clientOpts(cfg, slog.Default())
	cl, err := kgo.NewClient(opts...)
	require.NoError(t, err, "kgo.NewClient must succeed — the dial happens lazily")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = cl.Ping(ctx)

	require.Error(t, err, "Ping to a blocked address must fail")
	assert.True(t, errors.Is(err, netguard.ErrBlockedAddress),
		"expected ErrBlockedAddress for adhoc cluster dialing blocked broker, got: %v", err)
}

// TestClientOpts_ConfiguredCluster_NoSSRFGuard verifies that an
// operator-configured cluster does NOT have the SSRF guard applied.
// It dials 127.0.0.1:9 (the discard port — very unlikely to have a listener)
// and expects a plain connection error (ECONNREFUSED or timeout), NOT
// ErrBlockedAddress.
func TestClientOpts_ConfiguredCluster_NoSSRFGuard(t *testing.T) {
	t.Parallel()

	// Use a loopback address with a port almost certainly not listening.
	// The configured (non-adhoc) client must attempt the dial and get a
	// network error — not an SSRF refusal.
	cfg := config.ClusterConfig{
		Name:    configuredName(),
		Brokers: []string{"127.0.0.1:19299"}, // unlikely to have a listener
	}
	opts := clientOpts(cfg, slog.Default())
	cl, err := kgo.NewClient(opts...)
	require.NoError(t, err)
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = cl.Ping(ctx)

	require.Error(t, err, "Ping to a non-listening port must fail")
	assert.False(t, errors.Is(err, netguard.ErrBlockedAddress),
		"configured cluster must NOT trigger ErrBlockedAddress, got: %v", err)
}

// TestClientOpts_AdhocCluster_TLSEnabled_ConstructsWithoutConflict is the
// regression test for the Dialer+DialTLSConfig conflict. An ad-hoc cluster with
// TLS.Enabled previously appended BOTH kgo.Dialer and kgo.DialTLSConfig, which
// franz-go rejects at kgo.NewClient with "cannot set both Dialer and
// DialTLSConfig". TLS is the common case for cloud/user-supplied brokers, so
// this broke every operation for ad-hoc TLS clusters. NewClient does not dial,
// so no real broker is needed.
func TestClientOpts_AdhocCluster_TLSEnabled_ConstructsWithoutConflict(t *testing.T) {
	t.Parallel()

	cfg := config.ClusterConfig{
		Name:    adhocName(),
		Brokers: []string{"broker.example.com:9093"},
	}
	cfg.TLS.Enabled = true

	opts := clientOpts(cfg, slog.Default())
	cl, err := kgo.NewClient(opts...)

	require.NoError(t, err,
		"adhoc TLS cluster must construct without the Dialer+DialTLSConfig conflict")
	require.NotNil(t, cl)
	cl.Close()
}

// TestClientOpts_AdhocCluster_TLSInsecure_ConstructsWithoutConflict covers the
// same regression with InsecureSkipVerify set (common for self-signed dev
// brokers supplied via the X-Kafkito-Cluster header).
func TestClientOpts_AdhocCluster_TLSInsecure_ConstructsWithoutConflict(t *testing.T) {
	t.Parallel()

	cfg := config.ClusterConfig{
		Name:    adhocName(),
		Brokers: []string{"broker.example.com:9093"},
	}
	cfg.TLS.Enabled = true
	cfg.TLS.InsecureSkipVerify = true

	opts := clientOpts(cfg, slog.Default())
	cl, err := kgo.NewClient(opts...)

	require.NoError(t, err,
		"adhoc TLS (insecure) cluster must construct without the Dialer+DialTLSConfig conflict")
	require.NotNil(t, cl)
	cl.Close()
}

// TestClientOpts_ConfiguredCluster_TLSEnabled_UsesDialTLSConfigPath confirms the
// operator (non-ad-hoc) TLS path is unchanged: it still constructs cleanly via
// kgo.DialTLSConfig (no guarded dialer). The construction succeeding without a
// conflict implies DialTLSConfig was used without a competing kgo.Dialer.
func TestClientOpts_ConfiguredCluster_TLSEnabled_UsesDialTLSConfigPath(t *testing.T) {
	t.Parallel()

	cfg := config.ClusterConfig{
		Name:    configuredName(),
		Brokers: []string{"broker.example.com:9093"},
	}
	cfg.TLS.Enabled = true

	opts := clientOpts(cfg, slog.Default())
	cl, err := kgo.NewClient(opts...)

	require.NoError(t, err, "operator TLS cluster must construct cleanly via DialTLSConfig")
	require.NotNil(t, cl)
	cl.Close()
}

// TestAdhocPrefixMatchesConfigConstant guards the finding-#1 safety invariant:
// the ad-hoc detection in pkg/kafka (AdhocPrefix) and the validation guard in
// pkg/config (AdhocClusterPrefix) are two independent "__adhoc_" constants.
// They must stay equal or operator-config validation and ad-hoc routing could
// disagree about which names are reserved.
func TestAdhocPrefixMatchesConfigConstant(t *testing.T) {
	t.Parallel()

	assert.Equal(t, config.AdhocClusterPrefix, AdhocPrefix,
		"pkg/kafka.AdhocPrefix and pkg/config.AdhocClusterPrefix must stay equal")
}
