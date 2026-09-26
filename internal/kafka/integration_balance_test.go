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

// TestIntegration_Consume_FromEnd_BalancedAcrossPartitions checks that the
// "last N across all partitions" page is exactly the N newest records of the
// topic, ordered newest-first, whatever the distribution across partitions:
//
//   - interleaved: records alternate between partitions (60/30/10), so every
//     partition must show up in the page (the original kafka-ui parity goal:
//     a single partition must not dominate just because its broker answered
//     first);
//   - bursts: each partition is written in one burst and the last burst is
//     the largest, so the newest page belongs to a single partition and must
//     not be padded with older records of the other partitions.
//
// Records are produced sequentially, so a partition's burst order is
// deterministic. The expected page is derived from the topic itself by
// reading every record and sorting newest-first.
func TestIntegration_Consume_FromEnd_BalancedAcrossPartitions(t *testing.T) {
	broker := startBroker(t)
	reg := newRegistry(t, broker)

	scenarios := map[string][]int32{
		"interleaved": interleaved(10, []int32{0, 1, 0, 2, 0, 1, 0, 1, 0, 0}),
		"bursts":      append(append(repeat(0, 60), repeat(2, 10)...), repeat(1, 30)...),
	}
	for name, order := range scenarios {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			topic := "it-consume-balance-" + name
			require.NoError(t, reg.CreateTopic(ctx, "it", CreateTopicRequest{Name: topic, Partitions: 3, ReplicationFactor: 1}))
			for i, p := range order {
				_, err := reg.Produce(ctx, "it", topic, ProduceRequest{
					Partition: int32Ptr(p),
					Key:       fmt.Sprintf("k%03d", i),
					Value:     fmt.Sprintf(`{"p":%d,"i":%d}`, p, i),
				})
				require.NoError(t, err)
			}

			want := newestAcrossPartitions(ctx, t, reg, topic, 3, 30)
			res, err := reg.ConsumeMessages(ctx, "it", topic, ConsumeOptions{
				Partition: -1, Limit: 30, From: FromEnd, Timeout: 8 * time.Second,
			})
			require.NoError(t, err)
			require.False(t, res.Partial)
			require.Equal(t, positions(want), positions(res.Messages), "page must be the 30 newest records, newest first")

			if name == "interleaved" {
				got := map[int32]int{}
				for _, m := range res.Messages {
					got[m.Partition]++
				}
				require.Len(t, got, 3, "every partition must be represented (got %v)", got)
			}
		})
	}
}

// newestAcrossPartitions reads every record of topic and returns the n
// newest, ordered newest-first (timestamp desc, partition asc, offset desc).
func newestAcrossPartitions(ctx context.Context, t *testing.T, reg *Registry, topic string, partitions int32, n int) []Message {
	t.Helper()
	var all []Message
	for p := range partitions {
		res, err := reg.ConsumeMessages(ctx, "it", topic, ConsumeOptions{Partition: p, Limit: 500, From: FromStart, Timeout: 8 * time.Second})
		require.NoError(t, err)
		require.False(t, res.HasMore)
		all = append(all, res.Messages...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Timestamp != all[j].Timestamp {
			return all[i].Timestamp > all[j].Timestamp
		}
		if all[i].Partition != all[j].Partition {
			return all[i].Partition < all[j].Partition
		}
		return all[i].Offset > all[j].Offset
	})
	return all[:n]
}

func positions(msgs []Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, fmt.Sprintf("p%d@%d", m.Partition, m.Offset))
	}
	return out
}

func interleaved(rounds int, pattern []int32) []int32 {
	var out []int32
	for range rounds {
		out = append(out, pattern...)
	}
	return out
}

func repeat(p int32, n int) []int32 {
	out := make([]int32, n)
	for i := range out {
		out[i] = p
	}
	return out
}

// int32Ptr is a tiny helper for the integration tests in this file.
func int32Ptr(v int32) *int32 { return &v }
