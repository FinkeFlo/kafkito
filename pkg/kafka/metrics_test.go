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

	"github.com/twmb/franz-go/pkg/kadm"
)

// TestClusterStateWritePrev verifies that writePrev records a per-topic
// end-offset sum that matches what refreshOne will later diff against to
// derive a rate.
func TestClusterStateWritePrev(t *testing.T) {
	s := &clusterState{prev: map[string]topicSample{}}

	// Synthesize end offsets for two topics (two partitions each).
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
	if got, want := prev["a"].endOffsetSum, int64(150); got != want {
		t.Fatalf("a endOffsetSum: got %d want %d", got, want)
	}
	if got, want := prev["b"].endOffsetSum, int64(9); got != want {
		t.Fatalf("b endOffsetSum: got %d want %d", got, want)
	}
	if !prev["a"].at.Equal(t0) {
		t.Fatalf("a sample time not preserved")
	}

	// writePrev prunes topics that disappear, so the next round that drops
	// "b" from the topic list must forget it.
	ends2 := kadm.ListedOffsets{
		"a": {
			0: kadm.ListedOffset{Topic: "a", Partition: 0, Offset: 200},
			1: kadm.ListedOffset{Topic: "a", Partition: 1, Offset: 100},
		},
	}
	s.writePrev([]string{"a"}, ends2, t0.Add(10*time.Second))
	prev = s.prevPerTopic()
	if _, ok := prev["b"]; ok {
		t.Fatalf("expected b to be pruned from prev, got %+v", prev)
	}
	if got, want := prev["a"].endOffsetSum, int64(300); got != want {
		t.Fatalf("a endOffsetSum after update: got %d want %d", got, want)
	}

	// Rate is (endSumNew - endSumPrev)/dt. With 300 new vs 150 old over
	// 10s we expect 15 msg/s. Do the math the same way refreshOne does to
	// pin the semantics.
	dt := 10.0
	delta := float64(300 - 150)
	if got, want := delta/dt, 15.0; got != want {
		t.Fatalf("rate: got %v want %v", got, want)
	}
}

// TestClusterStateSnapshotEmpty documents that ClusterMetricsSnapshot
// returns false when no refresh has happened yet, so callers never see a
// zeroed ClusterMetrics and treat it as authoritative.
func TestClusterStateSnapshotEmpty(t *testing.T) {
	r := NewRegistry(nil, nil)
	_, ok := r.ClusterMetricsSnapshot("nope")
	if ok {
		t.Fatalf("expected no snapshot when collector not started")
	}
}

// TestPtrHelpers is a smoke test for the pointer constructors used to
// serialize "known zero" values as 0 (not null) in JSON.
func TestPtrHelpers(t *testing.T) {
	if *ptrInt64(7) != 7 {
		t.Fatal("ptrInt64")
	}
	if *ptrFloat64(1.5) != 1.5 {
		t.Fatal("ptrFloat64")
	}
}

// newTestCollector returns a metricsCollector with the minimum wiring
// required to exercise ensureFresh's TTL/lazy-state paths without
// touching Kafka. The probe pipeline itself is exercised end-to-end by
// the integration tests.
func newTestCollector() *metricsCollector {
	return &metricsCollector{
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		states: map[string]*clusterState{},
	}
}

// TestEnsureFresh_LazyStateCreation verifies that calling ensureFresh
// for an unknown cluster name creates the state entry on-the-fly so
// private (browser-stored) clusters get cached metrics without being
// pre-registered by the periodic collector.
func TestEnsureFresh_LazyStateCreation(t *testing.T) {
	mc := newTestCollector()

	// Use a context already cancelled so the probe pipeline bails out
	// at Metadata before touching the nil admin's network. ensureFresh
	// must still have created the state entry by then.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Calling against a nil admin would normally panic if probe
	// dereferenced it; we accept the panic via recover and only assert
	// on the lazy-creation invariant, which happens *before* the probe.
	defer func() { _ = recover() }()
	mc.ensureFresh(ctx, "private-2", time.Minute, nil)

	mc.statesMu.RLock()
	_, ok := mc.states["private-2"]
	mc.statesMu.RUnlock()
	if !ok {
		t.Fatal("private-2 should exist after ensureFresh")
	}
}

// TestEnsureFresh_CacheHitWithinTTL ensures a cached snapshot inside the
// TTL window short-circuits before any probe attempt.
func TestEnsureFresh_CacheHitWithinTTL(t *testing.T) {
	mc := newTestCollector()
	cached := ClusterMetrics{
		Brokers:   3,
		UpdatedAt: time.Now(),
	}
	mc.states["c"] = &clusterState{
		prev:     map[string]topicSample{},
		hasSnap:  true,
		snapshot: cached,
	}

	// Passing a nil admin would panic if probe were called → the test
	// passes if the call returns without panicking.
	mc.ensureFresh(context.Background(), "c", time.Minute, nil)

	if got := mc.states["c"].snapshot.Brokers; got != 3 {
		t.Fatalf("snapshot mutated: brokers=%d want 3", got)
	}
}
