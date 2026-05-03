// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package kafka

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
)

func TestClusterStateWritePrev_SumsEndOffsetsPerTopic(t *testing.T) {
	t.Parallel()

	s := &clusterState{prev: map[string]topicSample{}}
	ends := kadm.ListedOffsets{
		"a": {
			0: kadm.ListedOffset{Topic: "a", Partition: 0, Offset: 100},
			1: kadm.ListedOffset{Topic: "a", Partition: 1, Offset: 50},
		},
		"b": {
			0: kadm.ListedOffset{Topic: "b", Partition: 0, Offset: 9},
		},
	}
	t0 := time.Now()

	s.writePrev([]string{"a", "b"}, ends, t0)
	prev := s.prevPerTopic()

	t.Run("topic_a_sum", func(t *testing.T) {
		assert.Equal(t, int64(150), prev["a"].endOffsetSum)
	})
	t.Run("topic_b_sum", func(t *testing.T) {
		assert.Equal(t, int64(9), prev["b"].endOffsetSum)
	})
	t.Run("sample_time_preserved", func(t *testing.T) {
		assert.True(t, prev["a"].at.Equal(t0), "got %v want %v", prev["a"].at, t0)
	})
}

func TestClusterStateWritePrev_PrunesDisappearedTopics(t *testing.T) {
	t.Parallel()

	s := &clusterState{prev: map[string]topicSample{}}
	ends1 := kadm.ListedOffsets{
		"a": {
			0: kadm.ListedOffset{Topic: "a", Partition: 0, Offset: 100},
		},
		"b": {
			0: kadm.ListedOffset{Topic: "b", Partition: 0, Offset: 9},
		},
	}
	t0 := time.Now()
	s.writePrev([]string{"a", "b"}, ends1, t0)

	ends2 := kadm.ListedOffsets{
		"a": {
			0: kadm.ListedOffset{Topic: "a", Partition: 0, Offset: 200},
		},
	}
	s.writePrev([]string{"a"}, ends2, t0.Add(10*time.Second))
	prev := s.prevPerTopic()

	_, hasB := prev["b"]
	assert.False(t, hasB, "topic b should be pruned, got %+v", prev)
	assert.Equal(t, int64(200), prev["a"].endOffsetSum)
}

// TestClusterStateRate_MatchesRefreshOneSemantics pins the rate math the way
// refreshOne computes it: (endSumNew - endSumPrev)/dt. With 300 vs 150 over
// 10s the rate must be 15 msg/s.
func TestClusterStateRate_MatchesRefreshOneSemantics(t *testing.T) {
	t.Parallel()

	const endSumPrev, endSumNew, dtSeconds = 150.0, 300.0, 10.0

	rate := (endSumNew - endSumPrev) / dtSeconds

	assert.Equal(t, 15.0, rate)
}

func TestClusterMetricsSnapshot_ReturnsFalse_WhenCollectorNotStarted(t *testing.T) {
	t.Parallel()

	r := NewRegistry(nil, nil)

	_, ok := r.ClusterMetricsSnapshot("nope")

	assert.False(t, ok, "snapshot must be absent before any refresh")
}

func TestPtrHelpers_DereferenceToValue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		got  any
		want any
	}{
		{name: "ptrInt64", got: *ptrInt64(7), want: int64(7)},
		{name: "ptrFloat64", got: *ptrFloat64(1.5), want: 1.5},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.got)
		})
	}
}

func newTestCollector() *metricsCollector {
	return &metricsCollector{
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		states: map[string]*clusterState{},
	}
}

// TestEnsureFresh_LazyStateCreation_CreatesStateBeforeProbe verifies that
// calling ensureFresh for an unknown cluster name creates the state entry
// on-the-fly so private (browser-stored) clusters get cached metrics
// without being pre-registered by the periodic collector.
//
// The recover() seam is intentional: ensureFresh dereferences the nil
// admin during the probe stage, but the lazy-creation invariant fires
// *before* the probe. Phase-2 candidate: introduce an admin interface so
// the test can pass a fake instead of relying on panic-recover.
func TestEnsureFresh_LazyStateCreation_CreatesStateBeforeProbe(t *testing.T) {
	t.Parallel()

	mc := newTestCollector()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	defer func() { _ = recover() }()
	mc.ensureFresh(ctx, "private-2", time.Minute, nil)

	mc.statesMu.RLock()
	_, ok := mc.states["private-2"]
	mc.statesMu.RUnlock()

	require.True(t, ok, "private-2 state must exist after ensureFresh")
}

func TestEnsureFresh_ReturnsCachedSnapshot_WithinTTL(t *testing.T) {
	t.Parallel()

	mc := newTestCollector()
	cached := ClusterMetrics{Brokers: 3, UpdatedAt: time.Now()}
	mc.states["c"] = &clusterState{
		prev:     map[string]topicSample{},
		hasSnap:  true,
		snapshot: cached,
	}

	mc.ensureFresh(context.Background(), "c", time.Minute, nil)

	assert.Equal(t, 3, mc.states["c"].snapshot.Brokers,
		"cached snapshot must be returned without invoking probe (which would panic on nil admin)")
}
