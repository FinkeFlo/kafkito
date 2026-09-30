// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kmsg"

	"github.com/FinkeFlo/kafkito/internal/config"
	"github.com/FinkeFlo/kafkito/internal/connerr"
)

// newCapsCluster starts a kfake cluster and counts the DeleteTopics requests
// it receives. Every capability probe sends exactly one, so the counter is
// the number of probes that reached this cluster. With denyDelete the
// cluster answers them with TOPIC_AUTHORIZATION_FAILED, which makes its
// probe result distinguishable (DeleteTopic=false).
func newCapsCluster(t *testing.T, denyDelete bool) (*kfake.Cluster, *atomic.Int32) {
	t.Helper()
	c, err := kfake.NewCluster(kfake.NumBrokers(1))
	require.NoError(t, err)
	t.Cleanup(c.Close)

	var probes atomic.Int32
	c.ControlKey(int16(kmsg.DeleteTopics), func(kreq kmsg.Request) (kmsg.Response, error, bool) {
		probes.Add(1)
		if !denyDelete {
			return nil, nil, false
		}
		c.KeepControl()
		req := kreq.(*kmsg.DeleteTopicsRequest)
		resp := req.ResponseKind().(*kmsg.DeleteTopicsResponse)
		names := append([]string{}, req.TopicNames...)
		for _, rt := range req.Topics {
			if rt.Topic != nil {
				names = append(names, *rt.Topic)
			}
		}
		for _, name := range names {
			rt := kmsg.NewDeleteTopicsResponseTopic()
			rt.Topic = &name
			rt.ErrorCode = kerr.TopicAuthorizationFailed.Code
			resp.Topics = append(resp.Topics, rt)
		}
		return resp, nil, true
	})
	return c, &probes
}

// newCapsRegistry builds a Registry with one cluster named kfakeCluster that
// points at c.
func newCapsRegistry(t *testing.T, c *kfake.Cluster) *Registry {
	t.Helper()
	reg := NewRegistry(
		[]config.ClusterConfig{{Name: kfakeCluster, Brokers: c.ListenAddrs()}},
		slog.New(slog.DiscardHandler),
	)
	t.Cleanup(reg.Close)
	return reg
}

func capsCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// TestCapabilities_CachePerRegistry runs two registries side by side that use
// the same cluster name for two different Kafka clusters. Each must probe and
// cache its own cluster instead of serving the other's result.
func TestCapabilities_CachePerRegistry(t *testing.T) {
	t.Parallel()
	ctx := capsCtx(t)

	clusterA, probesA := newCapsCluster(t, false)
	clusterB, probesB := newCapsCluster(t, true)
	regA := newCapsRegistry(t, clusterA)
	regB := newCapsRegistry(t, clusterB)

	capsA, err := regA.Capabilities(ctx, kfakeCluster)
	require.NoError(t, err)
	assert.True(t, capsA.DeleteTopic, "cluster A allows deletes")

	capsB, err := regB.Capabilities(ctx, kfakeCluster)
	require.NoError(t, err)
	assert.False(t, capsB.DeleteTopic, "registry B served registry A's cached probe")
	assert.Equal(t, int32(1), probesB.Load(), "registry B must probe its own cluster")

	again, err := regA.Capabilities(ctx, kfakeCluster)
	require.NoError(t, err)
	assert.Same(t, capsA, again, "registry A keeps its own cache entry")
	assert.Equal(t, int32(1), probesA.Load())

	regB.RefreshCapabilities(kfakeCluster)
	again, err = regA.Capabilities(ctx, kfakeCluster)
	require.NoError(t, err)
	assert.Same(t, capsA, again, "refreshing registry B must not drop registry A's entry")
	assert.Equal(t, int32(1), probesA.Load())
}

// TestCapabilities_NewRegistryDoesNotServePreviousProbe mirrors a restart or
// reload: the first registry is closed and a new one is built for the same
// cluster name, now pointing at a different cluster. The new registry must
// probe instead of reusing the previous instance's result.
func TestCapabilities_NewRegistryDoesNotServePreviousProbe(t *testing.T) {
	t.Parallel()
	ctx := capsCtx(t)

	oldCluster, _ := newCapsCluster(t, false)
	newCluster, newProbes := newCapsCluster(t, true)

	oldReg := newCapsRegistry(t, oldCluster)
	oldCaps, err := oldReg.Capabilities(ctx, kfakeCluster)
	require.NoError(t, err)
	require.True(t, oldCaps.DeleteTopic)
	oldReg.Close()

	newReg := newCapsRegistry(t, newCluster)
	caps, err := newReg.Capabilities(ctx, kfakeCluster)
	require.NoError(t, err)
	assert.False(t, caps.DeleteTopic, "new registry served the previous registry's probe")
	assert.Equal(t, int32(1), newProbes.Load())
}

// TestCapabilities_CachedUntilRefreshOrTTL pins the cache semantics: a fresh
// entry is served without probing, RefreshCapabilities forces a new probe and
// so does an entry older than capCacheTTL.
func TestCapabilities_CachedUntilRefreshOrTTL(t *testing.T) {
	t.Parallel()
	ctx := capsCtx(t)

	c, probes := newCapsCluster(t, false)
	reg := newCapsRegistry(t, c)

	first, err := reg.Capabilities(ctx, kfakeCluster)
	require.NoError(t, err)
	require.Equal(t, int32(1), probes.Load())

	cached, err := reg.Capabilities(ctx, kfakeCluster)
	require.NoError(t, err)
	assert.Same(t, first, cached)
	assert.Equal(t, int32(1), probes.Load(), "fresh entry must not re-probe")

	reg.RefreshCapabilities(kfakeCluster)
	refreshed, err := reg.Capabilities(ctx, kfakeCluster)
	require.NoError(t, err)
	assert.NotSame(t, first, refreshed)
	assert.Equal(t, int32(2), probes.Load(), "refresh must re-probe")

	reg.mu.Lock()
	entry := reg.caps[kfakeCluster]
	entry.at = time.Now().Add(-capCacheTTL - time.Second)
	reg.caps[kfakeCluster] = entry
	reg.mu.Unlock()
	expired, err := reg.Capabilities(ctx, kfakeCluster)
	require.NoError(t, err)
	assert.NotSame(t, refreshed, expired)
	assert.Equal(t, int32(3), probes.Load(), "expired entry must re-probe")
}

func TestCapabilities_UnknownCluster(t *testing.T) {
	t.Parallel()
	reg := NewRegistry(nil, slog.New(slog.DiscardHandler))
	t.Cleanup(reg.Close)

	_, err := reg.Capabilities(context.Background(), "nope")
	require.ErrorIs(t, err, ErrUnknownCluster)
}

// TestCapabilities_NotCachedWhenEvictedDuringProbe removes the cluster while
// its probe is in flight, as the idle eviction of an ad-hoc cluster would. The
// probe result is still returned but must not be cached for a cluster that
// no longer exists.
func TestCapabilities_NotCachedWhenEvictedDuringProbe(t *testing.T) {
	t.Parallel()
	ctx := capsCtx(t)

	c, err := kfake.NewCluster(kfake.NumBrokers(1))
	require.NoError(t, err)
	t.Cleanup(c.Close)
	reg := newCapsRegistry(t, c)

	c.ControlKey(int16(kmsg.DeleteTopics), func(kmsg.Request) (kmsg.Response, error, bool) {
		reg.mu.Lock()
		delete(reg.clusters, kfakeCluster)
		reg.mu.Unlock()
		return nil, nil, false
	})

	caps, err := reg.Capabilities(ctx, kfakeCluster)
	require.NoError(t, err)
	require.NotNil(t, caps)

	reg.mu.Lock()
	_, cached := reg.caps[kfakeCluster]
	reg.mu.Unlock()
	assert.False(t, cached, "probe result cached for an evicted cluster")
}

// TestEvictIdleAdhoc_DropsCapabilitiesEntry checks that evicting an idle
// ad-hoc cluster also drops its cached capability probe, while entries of
// clusters that are still in use survive.
func TestEvictIdleAdhoc_DropsCapabilitiesEntry(t *testing.T) {
	t.Parallel()
	reg := NewRegistry(nil, slog.New(slog.DiscardHandler))
	t.Cleanup(reg.Close)

	idle, err := reg.UseAdhoc(config.ClusterConfig{Brokers: []string{"idle.invalid:9092"}})
	require.NoError(t, err)
	busy, err := reg.UseAdhoc(config.ClusterConfig{Brokers: []string{"busy.invalid:9092"}})
	require.NoError(t, err)
	require.True(t, config.IsAdhocClusterName(idle))

	reg.mu.Lock()
	reg.caps[idle] = capCache{caps: &Capabilities{}, at: time.Now()}
	reg.caps[busy] = capCache{caps: &Capabilities{}, at: time.Now()}
	reg.adhocLastUsed[idle] = time.Time{}
	reg.mu.Unlock()

	reg.evictIdleAdhoc()

	reg.mu.Lock()
	defer reg.mu.Unlock()
	_, idleCached := reg.caps[idle]
	_, busyCached := reg.caps[busy]
	assert.False(t, idleCached, "eviction left the capability entry of an evicted cluster")
	assert.True(t, busyCached, "eviction dropped the entry of a cluster still in use")
}

// TestCapabilities_NotCachedWhenRequestEndsDuringProbe cancels the caller's
// context while the probe runs. The probes that fail with that cancellation
// say nothing about the cluster, so the result must not be cached: the next
// caller probes again and sees the real capabilities.
func TestCapabilities_NotCachedWhenRequestEndsDuringProbe(t *testing.T) {
	c, probes := newCapsCluster(t, false)
	reg := newCapsRegistry(t, c)

	ctx, cancel := context.WithCancel(capsCtx(t))
	c.ControlKey(int16(kmsg.DeleteTopics), func(kmsg.Request) (kmsg.Response, error, bool) {
		cancel()
		return nil, nil, false
	})
	_, err := reg.Capabilities(ctx, kfakeCluster)
	require.ErrorIs(t, err, context.Canceled)

	caps, err := reg.Capabilities(capsCtx(t), kfakeCluster)
	require.NoError(t, err)
	assert.Equal(t, int32(2), probes.Load(), "the canceled probe must not be served from the cache")
	assert.True(t, caps.CreateTopic)
	assert.True(t, caps.AlterConfigs)
	assert.Empty(t, caps.Errors)
}

// On a private cluster a failed probe reports the class text of a
// connection error, never the address it failed to reach. Broker error
// codes and an empty broker list keep their own text; configured clusters
// keep the full error.
func TestCapabilityErr(t *testing.T) {
	t.Parallel()

	dialErr := fmt.Errorf("unable to dial: %w", &net.OpError{
		Op: "dial", Net: "tcp",
		Addr: &net.TCPAddr{IP: net.ParseIP("192.0.2.7"), Port: 9092},
		Err:  syscall.ECONNREFUSED,
	})
	tests := []struct {
		name    string
		err     error
		private bool
		want    string
	}{
		{"private dial error", dialErr, true, connerr.Refused.Message()},
		{"private broker code", kerr.ClusterAuthorizationFailed, true, kerr.ClusterAuthorizationFailed.Message},
		{"private no brokers", errNoBrokersReturned, true, "no brokers returned"},
		{"configured dial error", dialErr, false, dialErr.Error()},
		{"configured broker code", kerr.ClusterAuthorizationFailed, false, kerr.ClusterAuthorizationFailed.Message},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, capabilityErr(tc.err, tc.private))
		})
	}
}
