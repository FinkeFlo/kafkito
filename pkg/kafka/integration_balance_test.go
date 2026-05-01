//go:build integration
// +build integration

// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestIntegration_Consume_FromEnd_BalancedAcrossPartitions produces an
// uneven 60/30/10 distribution across 3 partitions and asserts that the
// "last 30 across all partitions" query returns records from every
// partition (proportionally), ordered newest-first.
//
// This test reproduces Issue 1: kafka-ui returns mixed partitions for
// the equivalent query, but kafkito's pre-fix implementation returned
// records biased to whichever partition's broker responded first.
func TestIntegration_Consume_FromEnd_BalancedAcrossPartitions(t *testing.T) {
	broker := startBroker(t)
	reg := newRegistry(t, broker)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	topic := "it-consume-balance"
	require.NoError(t, reg.CreateTopic(ctx, "it", CreateTopicRequest{
		Name:              topic,
		Partitions:        3,
		ReplicationFactor: 1,
	}))

	// Produce uneven counts across partitions: 60 to p0, 30 to p1, 10 to p2.
	dist := map[int32]int{0: 60, 1: 30, 2: 10}
	produced := 0
	for p, n := range dist {
		for i := 0; i < n; i++ {
			_, err := reg.Produce(ctx, "it", topic, ProduceRequest{
				Partition: int32Ptr(p),
				Key:       fmt.Sprintf("p%d-k%03d", p, i),
				Value:     fmt.Sprintf(`{"p":%d,"i":%d}`, p, i),
			})
			require.NoError(t, err)
			produced++
		}
	}
	require.Equal(t, 100, produced)

	// Wait briefly so all records are visible at end-offsets.
	time.Sleep(500 * time.Millisecond)

	// Pull the "last 30 across all partitions".
	res, err := reg.ConsumeMessages(ctx, "it", topic, ConsumeOptions{
		Partition: -1,
		Limit:     30,
		From:      FromEnd,
		Timeout:   8 * time.Second,
	})
	require.NoError(t, err)
	require.Len(t, res.Messages, 30, "want exactly 30 messages")

	// Coverage: every partition with at least 10 records must contribute
	// at least 4 records. The regression we are guarding against returned
	// ~30/0/0 (single partition dominated). The exact post-fix split
	// depends on per-partition timestamp clustering — produce-rate skew
	// often makes one partition's newest records temporally older than
	// another's, so after the global newest-first sort the higher-rate
	// partition can drop below its 1/K=10 share. Threshold of 4 still
	// proves "all three partitions represented", which is the user-
	// visible parity goal kafka-ui exhibits.
	got := map[int32]int{}
	for _, m := range res.Messages {
		got[m.Partition]++
	}
	for p, total := range dist {
		minWant := 4
		if total < minWant {
			minWant = total
		}
		require.GreaterOrEqualf(t, got[p], minWant,
			"partition %d should contribute at least %d records (got %d, dist %+v)",
			p, minWant, got[p], got)
	}

	// Ordering: newest-first by timestamp (ties broken by partition asc, offset desc).
	sorted := make([]Message, len(res.Messages))
	copy(sorted, res.Messages)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Timestamp != sorted[j].Timestamp {
			return sorted[i].Timestamp > sorted[j].Timestamp
		}
		if sorted[i].Partition != sorted[j].Partition {
			return sorted[i].Partition < sorted[j].Partition
		}
		return sorted[i].Offset > sorted[j].Offset
	})
	for i := range sorted {
		require.Equalf(t, sorted[i], res.Messages[i],
			"messages[%d] not in newest-first order", i)
	}
}

// int32Ptr is a tiny helper for the integration tests in this file.
func int32Ptr(v int32) *int32 { return &v }
