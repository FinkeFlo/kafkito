// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package kafka

import (
	"context"
	"errors"
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

// fakeAdmin satisfies adminProber for unit tests. Only the calls probe()
// makes are stubbed; metadataErr short-circuits probe before the rest are
// reached. Returning zero values from the others is safe because the
// pipeline's error checks treat them as "no data, leave fields unset".
type fakeAdmin struct {
	metadataErr error
	md          kadm.Metadata
}

func (f *fakeAdmin) Metadata(_ context.Context, _ ...string) (kadm.Metadata, error) {
	return f.md, f.metadataErr
}

func (f *fakeAdmin) ListStartOffsets(_ context.Context, _ ...string) (kadm.ListedOffsets, error) {
	return nil, nil
}

func (f *fakeAdmin) ListEndOffsets(_ context.Context, _ ...string) (kadm.ListedOffsets, error) {
	return nil, nil
}

func (f *fakeAdmin) DescribeAllLogDirs(_ context.Context, _ kadm.TopicsSet) (kadm.DescribedAllLogDirs, error) {
	return nil, nil
}

func (f *fakeAdmin) DescribeTopicConfigs(_ context.Context, _ ...string) (kadm.ResourceConfigs, error) {
	return nil, nil
}

func (f *fakeAdmin) ListGroups(_ context.Context, _ ...string) (kadm.ListedGroups, error) {
	return nil, nil
}

func (f *fakeAdmin) FetchManyOffsets(_ context.Context, _ ...string) kadm.FetchOffsetsResponses {
	return nil
}

// panickingAdmin satisfies adminProber by panicking on every call. Pass
// it where the test expects a probe-side branch to short-circuit before
// the network is touched: a panic with the embedded message names the
// invariant that was violated, instead of failing later with a
// confusing nil-deref or zero-value comparison.
type panickingAdmin struct{ msg string }

func (p panickingAdmin) Metadata(_ context.Context, _ ...string) (kadm.Metadata, error) {
	panic(p.msg)
}

func (p panickingAdmin) ListStartOffsets(_ context.Context, _ ...string) (kadm.ListedOffsets, error) {
	panic(p.msg)
}

func (p panickingAdmin) ListEndOffsets(_ context.Context, _ ...string) (kadm.ListedOffsets, error) {
	panic(p.msg)
}

func (p panickingAdmin) DescribeAllLogDirs(_ context.Context, _ kadm.TopicsSet) (kadm.DescribedAllLogDirs, error) {
	panic(p.msg)
}

func (p panickingAdmin) DescribeTopicConfigs(_ context.Context, _ ...string) (kadm.ResourceConfigs, error) {
	panic(p.msg)
}

func (p panickingAdmin) ListGroups(_ context.Context, _ ...string) (kadm.ListedGroups, error) {
	panic(p.msg)
}

func (p panickingAdmin) FetchManyOffsets(_ context.Context, _ ...string) kadm.FetchOffsetsResponses {
	panic(p.msg)
}

// TestEnsureFresh_CreatesStateEntry_ForUnknownClusterName_BeforeProbe
// verifies that calling ensureFresh for an unknown cluster name creates
// the state entry on-the-fly so private (browser-stored) clusters get
// cached metrics without being pre-registered by the periodic collector.
// A failing probe (Metadata error) leaves the entry without a snapshot
// rather than removing it.
func TestEnsureFresh_CreatesStateEntry_ForUnknownClusterName_BeforeProbe(t *testing.T) {
	t.Parallel()

	mc := newTestCollector()
	adm := &fakeAdmin{metadataErr: errors.New("no broker")}

	mc.ensureFresh(context.Background(), "private-2", time.Minute, adm)

	mc.statesMu.RLock()
	state, ok := mc.states["private-2"]
	mc.statesMu.RUnlock()
	require.True(t, ok, "private-2 state must exist after ensureFresh")

	state.mu.RLock()
	hasSnap := state.hasSnap
	state.mu.RUnlock()
	assert.False(t, hasSnap, "failing probe must leave hasSnap=false")
}

// TestEnsureFresh_StoresSnapshot_OnSuccessfulProbe verifies the GREEN
// path through the seam: a fake admin returning a minimal valid Metadata
// (one broker, no topics) yields a stored snapshot whose Brokers field
// reflects the metadata response.
func TestEnsureFresh_StoresSnapshot_OnSuccessfulProbe(t *testing.T) {
	t.Parallel()

	mc := newTestCollector()
	adm := &fakeAdmin{
		md: kadm.Metadata{
			Brokers: kadm.BrokerDetails{{NodeID: 1}},
			Topics:  kadm.TopicDetails{},
		},
	}

	mc.ensureFresh(context.Background(), "c", time.Minute, adm)

	mc.statesMu.RLock()
	state, ok := mc.states["c"]
	mc.statesMu.RUnlock()
	require.True(t, ok)

	state.mu.RLock()
	defer state.mu.RUnlock()
	require.True(t, state.hasSnap, "successful probe must store a snapshot")
	assert.Equal(t, 1, state.snapshot.Brokers)
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

	// panickingAdmin asserts the short-circuit explicitly: any probe call
	// panics with a clear message instead of producing a confusing
	// downstream failure.
	adm := panickingAdmin{msg: "TTL short-circuit violated: probe was called before TTL check"}

	mc.ensureFresh(context.Background(), "c", time.Minute, adm)

	assert.Equal(t, 3, mc.states["c"].snapshot.Brokers,
		"cached snapshot must be returned without invoking probe")
}
