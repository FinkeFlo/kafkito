// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kfake"

	"github.com/FinkeFlo/kafkito/internal/config"
)

// fakeClock is a settable clock for the ad-hoc last-use times. reads counts
// the calls to Now, which tells a test how often the janitor ran while
// nothing else reads the clock.
type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	reads atomic.Int64
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.reads.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// clockedRegistry is a Registry whose ad-hoc entries age on clock and whose
// janitor runs every sweepEvery. Private clusters dial directly, so a kfake
// cluster on loopback is reachable.
func clockedRegistry(t *testing.T, clock *fakeClock, sweepEvery time.Duration) *Registry {
	t.Helper()
	reg := NewRegistry(nil, slog.New(slog.DiscardHandler))
	t.Cleanup(reg.Close)
	reg.now = clock.Now
	reg.adhocSweepEvery = sweepEvery
	reg.adhocDial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	return reg
}

func janitorArmed(r *Connections) bool {
	r.janitorMu.Lock()
	defer r.janitorMu.Unlock()
	return r.janitor != nil
}

func hasMetricsState(t *testing.T, r *Registry, cluster string) bool {
	t.Helper()
	mc := r.metricsCollector()
	require.NotNil(t, mc, "metrics collector not started")
	mc.statesMu.RLock()
	defer mc.statesMu.RUnlock()
	_, ok := mc.states[cluster]
	return ok
}

func hasTopicConfigs(r *Registry, cluster string) bool {
	r.cfgCacheMu.Lock()
	defer r.cfgCacheMu.Unlock()
	for key := range r.cfgCache {
		if c, _, _ := strings.Cut(key, "\x00"); c == cluster {
			return true
		}
	}
	return false
}

// waitForJanitorRuns waits until the janitor has read the clock n more
// times, that is, has run at least n more times.
func waitForJanitorRuns(t *testing.T, clock *fakeClock, n int64) {
	t.Helper()
	target := clock.reads.Load() + n
	require.Eventually(t, func() bool { return clock.reads.Load() >= target },
		5*time.Second, time.Millisecond, "janitor did not run")
}

// The janitor evicts an idle private cluster on its own: no further
// UseAdhoc call (or any other request) is needed.
func TestJanitor_EvictsIdleAdhocWithoutFurtherRequests(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	reg := clockedRegistry(t, clock, time.Millisecond)
	cfg := config.ClusterConfig{Brokers: []string{"idle.invalid:9092"}}

	name, err := reg.UseAdhoc(cfg)
	require.NoError(t, err)
	_, err = reg.Client(name)
	require.NoError(t, err)
	require.True(t, janitorArmed(reg.Connections), "registration must arm the janitor")

	clock.Advance(adhocIdleTTL - time.Second)
	waitForJanitorRuns(t, clock, 3)
	require.True(t, reg.registered(name), "evicted before the idle TTL")

	clock.Advance(time.Second)
	require.Eventually(t, func() bool { return !reg.registered(name) },
		5*time.Second, time.Millisecond, "janitor kept a cluster idle for the TTL")
	reg.mu.Lock()
	_, client := reg.clients[name]
	_, lastUse := reg.adhocLastUsed[name]
	reg.mu.Unlock()
	assert.False(t, client, "client kept for an evicted cluster")
	assert.False(t, lastUse, "last use kept for an evicted cluster")

	// With no private cluster left the janitor stays off until the next
	// registration.
	require.Eventually(t, func() bool { return !janitorArmed(reg.Connections) },
		5*time.Second, time.Millisecond, "janitor still armed without private clusters")
	_, err = reg.UseAdhoc(cfg)
	require.NoError(t, err)
	assert.True(t, janitorArmed(reg.Connections), "a new registration must arm the janitor again")
}

// Every per-request accessor counts as a use and postpones the eviction.
func TestAdhocAccessorsPostponeEviction(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		use  func(t *testing.T, reg *Registry, name string)
	}{
		{"UseAdhoc", func(t *testing.T, reg *Registry, name string) {
			got, err := reg.UseAdhoc(config.ClusterConfig{Brokers: []string{"used.invalid:9092"}})
			require.NoError(t, err)
			require.Equal(t, name, got)
		}},
		{"Client", func(t *testing.T, reg *Registry, name string) {
			_, err := reg.Client(name)
			require.NoError(t, err)
		}},
		{"Admin", func(t *testing.T, reg *Registry, name string) {
			_, err := reg.Admin(name)
			require.NoError(t, err)
		}},
		{"ConfigFor", func(t *testing.T, reg *Registry, name string) {
			_, ok := reg.ConfigFor(name)
			require.True(t, ok)
		}},
		{"MaskingPolicy", func(t *testing.T, reg *Registry, name string) {
			require.NotNil(t, reg.MaskingPolicy(name))
		}},
		{"Capabilities", func(t *testing.T, reg *Registry, name string) {
			// A cached result keeps the call off the network.
			reg.mu.Lock()
			reg.caps[name] = capCache{caps: &Capabilities{}, at: time.Now()}
			reg.mu.Unlock()
			_, err := reg.Capabilities(t.Context(), name)
			require.NoError(t, err)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clock := newFakeClock()
			// The janitor is kept out of the way; the test evicts by hand.
			reg := clockedRegistry(t, clock, time.Hour)
			name, err := reg.UseAdhoc(config.ClusterConfig{Brokers: []string{"used.invalid:9092"}})
			require.NoError(t, err)

			clock.Advance(10 * time.Minute)
			tc.use(t, reg, name)
			clock.Advance(adhocIdleTTL - time.Second)
			reg.evictIdleAdhoc()
			require.True(t, reg.registered(name), "evicted although used within the TTL")

			clock.Advance(time.Second)
			reg.evictIdleAdhoc()
			assert.False(t, reg.registered(name), "kept although idle for the TTL")
		})
	}
}

// Eviction drops what the services keep per cluster: the collected metrics
// and the cached topic configs. It closes the client. A private cluster that
// is still in use keeps all of it.
func TestEvictIdleAdhoc_DropsMetricsAndTopicConfigs(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	clock := newFakeClock()
	reg := clockedRegistry(t, clock, time.Hour)
	reg.StartMetrics(ctx, time.Hour)

	register := func() string {
		c, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(1, "orders"))
		require.NoError(t, err)
		t.Cleanup(c.Close)
		name, err := reg.UseAdhoc(config.ClusterConfig{Brokers: c.ListenAddrs()})
		require.NoError(t, err)
		_, err = reg.ListTopics(ctx, name)
		require.NoError(t, err)
		_, err = reg.DescribeTopic(ctx, name, "orders")
		require.NoError(t, err)
		require.True(t, hasMetricsState(t, reg, name), "ListTopics must collect metrics")
		require.True(t, hasTopicConfigs(reg, name), "DescribeTopic must cache topic configs")
		return name
	}
	idle, busy := register(), register()
	idleClient, err := reg.Client(idle)
	require.NoError(t, err)

	clock.Advance(adhocIdleTTL - time.Minute)
	_, err = reg.Client(busy)
	require.NoError(t, err)
	clock.Advance(time.Minute)
	reg.evictIdleAdhoc()

	assert.False(t, reg.registered(idle), "idle cluster not evicted")
	assert.False(t, hasMetricsState(t, reg, idle), "metrics kept for an evicted cluster")
	assert.False(t, hasTopicConfigs(reg, idle), "topic configs kept for an evicted cluster")
	require.ErrorIs(t, idleClient.Context().Err(), context.Canceled, "client of an evicted cluster not closed")

	assert.True(t, reg.registered(busy), "cluster in use evicted")
	assert.True(t, hasMetricsState(t, reg, busy), "metrics of a cluster in use dropped")
	assert.True(t, hasTopicConfigs(reg, busy), "topic configs of a cluster in use dropped")
}

// The periodic metrics refresh is not a use: it must not keep an idle
// private cluster alive.
func TestMetricsRefresh_DoesNotPostponeEviction(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(1, "orders"))
	require.NoError(t, err)
	t.Cleanup(c.Close)
	clock := newFakeClock()
	reg := clockedRegistry(t, clock, time.Hour)
	reg.StartMetrics(ctx, time.Hour)
	name, err := reg.UseAdhoc(config.ClusterConfig{Brokers: c.ListenAddrs()})
	require.NoError(t, err)
	_, err = reg.ListTopics(ctx, name)
	require.NoError(t, err)

	clock.Advance(adhocIdleTTL)
	mc := reg.metricsCollector()
	mc.refreshOne(name)
	snap, ok := reg.ClusterMetricsSnapshot(name)
	require.True(t, ok, "refresh must still collect metrics")
	require.Equal(t, 1, snap.Brokers)

	reg.evictIdleAdhoc()
	assert.False(t, reg.registered(name), "metrics refresh kept an idle cluster alive")
}

// A cache entry written for a private cluster that was evicted while the
// request ran is dropped again instead of staying behind.
func TestCacheWritesForEvictedAdhocAreDropped(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(1, "orders"))
	require.NoError(t, err)
	t.Cleanup(c.Close)
	reg := clockedRegistry(t, newFakeClock(), time.Hour)
	reg.StartMetrics(ctx, time.Hour)
	live, err := reg.UseAdhoc(config.ClusterConfig{Brokers: c.ListenAddrs()})
	require.NoError(t, err)
	adm, err := reg.Admin(live)
	require.NoError(t, err)
	gone := config.AdhocClusterPrefix + "0000000000000000"

	reg.describeCachedTopicConfigs(ctx, gone, "orders", adm)
	reg.metricsCollector().ensureFresh(ctx, gone, time.Minute, adm)

	assert.False(t, hasTopicConfigs(reg, gone), "topic configs cached for an evicted cluster")
	assert.False(t, hasMetricsState(t, reg, gone), "metrics kept for an evicted cluster")
}

// Close stops the janitor for good: it does not run afterwards, and a later
// registration does not arm it again.
func TestJanitor_StopsOnClose(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	reg := NewRegistry(nil, slog.New(slog.DiscardHandler))
	reg.now = clock.Now
	reg.adhocSweepEvery = time.Millisecond
	name, err := reg.UseAdhoc(config.ClusterConfig{Brokers: []string{"closed.invalid:9092"}})
	require.NoError(t, err)
	waitForJanitorRuns(t, clock, 1)

	reg.Close()
	assert.False(t, janitorArmed(reg.Connections), "janitor armed after Close")

	reads := clock.reads.Load()
	clock.Advance(adhocIdleTTL)
	time.Sleep(20 * reg.adhocSweepEvery)
	assert.Equal(t, reads, clock.reads.Load(), "janitor ran after Close")
	assert.True(t, reg.registered(name), "janitor evicted after Close")

	_, err = reg.UseAdhoc(config.ClusterConfig{Brokers: []string{"later.invalid:9092"}})
	require.NoError(t, err)
	assert.False(t, janitorArmed(reg.Connections), "registration after Close armed the janitor")
}

// Close waits for an eviction in progress, so no eviction runs once Close
// has returned.
func TestJanitor_CloseWaitsForRunningEviction(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	reg := NewRegistry(nil, slog.New(slog.DiscardHandler))
	reg.now = clock.Now
	reg.adhocSweepEvery = time.Millisecond
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	reg.evictHooks = append(reg.evictHooks, func([]string) {
		once.Do(func() { close(entered) })
		<-release
	})
	_, err := reg.UseAdhoc(config.ClusterConfig{Brokers: []string{"slow.invalid:9092"}})
	require.NoError(t, err)

	clock.Advance(adhocIdleTTL)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("janitor did not evict")
	}
	closed := make(chan struct{})
	go func() {
		reg.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned during an eviction")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the eviction finished")
	}
}
