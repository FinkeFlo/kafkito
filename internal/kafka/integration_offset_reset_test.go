//go:build integration
// +build integration

// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration_OutOfRangeOffsets pins what the reader clients do with an
// offset that a real broker answers with OFFSET_OUT_OF_RANGE: below the log
// start after DeleteRecords, and past the log end.
func TestIntegration_OutOfRangeOffsets(t *testing.T) {
	broker := startBroker(t)
	reg := newRegistry(t, broker)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	const topic = "it-out-of-range"
	require.NoError(t, reg.CreateTopic(ctx, "it", CreateTopicRequest{Name: topic, Partitions: 1, ReplicationFactor: 1}))
	p := int32(0)
	reqs := make([]ProduceRequest, 10)
	for i := range reqs {
		reqs[i] = ProduceRequest{Partition: &p, Value: fmt.Sprintf("v-%d", i)}
	}
	n, err := reg.ProduceBatch(ctx, "it", topic, reqs)
	require.NoError(t, err)
	require.Equal(t, 10, n)
	del, err := reg.DeleteRecords(ctx, "it", topic, DeleteRecordsRequest{Partitions: map[int32]int64{0: 4}})
	require.NoError(t, err)
	require.Len(t, del, 1)
	require.Empty(t, del[0].Error)

	t.Run("scan range below log start resumes at log start", func(t *testing.T) {
		cfg, ok := reg.ConfigFor("it")
		require.True(t, ok)
		var offsets []int64
		for batch, err := range reg.scanRecords(ctx, recordScan{
			cluster: "it", topic: topic, role: "test", cfg: cfg,
			ranges: map[int32]PartitionRange{0: {Start: 0, End: 10}}, drainAfter: 2,
		}) {
			require.NoError(t, err)
			for _, rec := range batch.records {
				offsets = append(offsets, rec.Offset)
			}
		}
		assert.Equal(t, []int64{4, 5, 6, 7, 8, 9}, offsets)
	})

	t.Run("raw download of a deleted offset is not found", func(t *testing.T) {
		_, err := reg.FetchRawMessageValue(ctx, "it", topic, 0, 2)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "record not found")
	})

	// The reset to the log start re-reads offsets 4 to 9, none of which is
	// the requested one, and then waits for offset 100 until the request
	// ends. It never returns another record.
	t.Run("raw download past the log end waits for the request to end", func(t *testing.T) {
		fctx, fcancel := context.WithTimeout(ctx, 3*time.Second)
		defer fcancel()
		raw, err := reg.FetchRawMessageValue(fctx, "it", topic, 0, 100)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Nil(t, raw)
	})

	t.Run("consume below log start starts at log start", func(t *testing.T) {
		res, err := reg.ConsumeMessages(ctx, "it", topic, ConsumeOptions{Partition: 0, From: FromOffset, Offset: 0, Limit: 3, Timeout: 5 * time.Second})
		require.NoError(t, err)
		require.Len(t, res.Messages, 3)
		assert.Equal(t, "v-4", res.Messages[0].Value)
	})
}
