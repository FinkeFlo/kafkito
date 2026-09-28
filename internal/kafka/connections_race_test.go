package kafka

import (
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
)

// TestConnections_AdhocChurnRacesWithReaders registers, evicts and re-registers
// ad-hoc clusters while other goroutines read the same clusters through every
// lookup that touches the shared maps. Run with -race: an unlocked read shows
// up as a data race (or a fatal concurrent map access). The timeout guards the
// mu -> srMu lock order against deadlocks.
func TestConnections_AdhocChurnRacesWithReaders(t *testing.T) {
	t.Parallel()

	r := NewRegistry(nil, slog.New(slog.DiscardHandler))
	t.Cleanup(r.Close)

	const clusters = 8
	cfgs := make([]config.ClusterConfig, clusters)
	names := make([]string, clusters)
	for i := range cfgs {
		cfgs[i] = config.ClusterConfig{
			Brokers:        []string{fmt.Sprintf("broker-%d.invalid:9092", i)},
			SchemaRegistry: config.SchemaRegistryConfig{URL: fmt.Sprintf("http://sr-%d.invalid", i)},
		}
		name, err := r.UseAdhoc(cfgs[i])
		require.NoError(t, err)
		names[i] = name
	}

	// Backdating the last-use stamps makes the next UseAdhoc sweep evict
	// every entry, so the writers keep deleting and re-adding map entries.
	// A capability entry is seeded for each cluster so the sweep drops it
	// too.
	expireAll := func() {
		r.mu.Lock()
		for name := range r.adhocLastUsed {
			r.adhocLastUsed[name] = time.Time{}
			r.caps[name] = capCache{caps: &Capabilities{}, at: time.Now()}
		}
		r.mu.Unlock()
	}

	const iterations = 300
	var wg sync.WaitGroup
	wg.Go(func() {
		for range iterations {
			expireAll()
		}
	})
	for w := range 2 {
		wg.Go(func() {
			for i := range iterations {
				_, err := r.UseAdhoc(cfgs[(i+w)%clusters])
				assert.NoError(t, err)
			}
		})
	}
	for rd := range 4 {
		wg.Go(func() {
			for i := range iterations {
				name := names[(i+rd)%clusters]
				_, _ = r.SchemaRegistry(name)
				_ = r.srDecoderFor(name)
				assert.NotNil(t, r.MaskingPolicy(name))
				_, _ = r.ConfigFor(name)
				r.RefreshCapabilities(name)
			}
		})
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("adhoc churn did not finish: possible deadlock between mu and srMu")
	}

	// Once quiet, a final sweep leaves no cached decoder behind for a
	// cluster that is no longer registered.
	expireAll()
	_, err := r.UseAdhoc(config.ClusterConfig{Brokers: []string{"final.invalid:9092"}})
	require.NoError(t, err)

	r.mu.Lock()
	registered := make(map[string]bool, len(r.clusters))
	for name := range r.clusters {
		registered[name] = true
	}
	for name := range r.caps {
		assert.Truef(t, registered[name], "capabilities cached for evicted cluster %s", name)
	}
	r.mu.Unlock()
	r.srMu.Lock()
	defer r.srMu.Unlock()
	for name := range r.srDecoders {
		assert.Truef(t, registered[name], "decoder cached for evicted cluster %s", name)
	}
}
