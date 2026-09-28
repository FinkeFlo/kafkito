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
	"github.com/twmb/franz-go/pkg/kgo"
)

// The tests in this file pin what the short-lived reader clients do when a
// requested offset is out of range: the broker answers OFFSET_OUT_OF_RANGE
// and the client resumes at the log start, never at the end and never by
// rewinding in time.

const truncatedTopic = "truncated"

// newTruncatedEnv produces ten records to a single-partition topic and then
// deletes the first four, so the log start is 4 and the log end is 10.
func newTruncatedEnv(t *testing.T) *kfakeEnv {
	t.Helper()
	env := newKfakeEnv(t, truncatedTopic, 1, nil)
	for i := range 10 {
		env.produce(t, &kgo.Record{Partition: 0, Value: fmt.Appendf(nil, "v-%d", i)})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := env.reg.DeleteRecords(ctx, kfakeCluster, truncatedTopic, DeleteRecordsRequest{Partitions: map[int32]int64{0: 4}})
	require.NoError(t, err)
	require.Len(t, res, 1)
	require.Empty(t, res[0].Error)
	require.EqualValues(t, 4, res[0].LowWatermark)
	return env
}

// scanAll runs a forward scan of [start, end) on partition 0 and returns the
// offsets of every yielded record.
func scanAll(t *testing.T, env *kfakeEnv, start, end int64) []int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg, ok := env.reg.ConfigFor(kfakeCluster)
	require.True(t, ok)
	var offsets []int64
	for batch, err := range env.reg.scanRecords(ctx, recordScan{
		cluster: kfakeCluster, topic: truncatedTopic, role: "test", cfg: cfg,
		ranges: map[int32]PartitionRange{0: {Start: start, End: end}}, drainAfter: 2,
	}) {
		require.NoError(t, err)
		for _, rec := range batch.records {
			offsets = append(offsets, rec.Offset)
		}
	}
	return offsets
}

// A range that starts below the log start is what a scan sees when
// retention or DeleteRecords advances the log start between listing the
// offsets and the first fetch. The scan resumes at the log start and still
// returns every surviving record of the range.
func TestScanRecords_RangeBelowLogStartResumesAtLogStart(t *testing.T) {
	env := newTruncatedEnv(t)
	assert.Equal(t, []int64{4, 5, 6, 7, 8, 9}, scanAll(t, env, 0, 10))
}

// A range entirely below the log start yields nothing and ends; the scan
// neither hangs nor skips to records outside the range.
func TestScanRecords_RangeFullyBelowLogStartIsEmpty(t *testing.T) {
	env := newTruncatedEnv(t)
	assert.Empty(t, scanAll(t, env, 0, 3))
}

// FetchRawMessageValue does not clamp the requested offset, so a deleted
// offset reaches the broker as-is. The answer is "not found", not a record
// from elsewhere in the log.
func TestFetchRawMessageValue_DeletedOffsetIsNotFound(t *testing.T) {
	env := newTruncatedEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := env.reg.FetchRawMessageValue(ctx, kfakeCluster, truncatedTopic, 0, 2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "record not found")

	raw, err := env.reg.FetchRawMessageValue(ctx, kfakeCluster, truncatedTopic, 0, 4)
	require.NoError(t, err)
	assert.Equal(t, "v-4", string(raw.Value))
}

// ConsumeMessages clamps a from=offset request below the log start to the
// log start.
func TestConsumeMessages_OffsetBelowLogStartStartsAtLogStart(t *testing.T) {
	env := newTruncatedEnv(t)
	res := consumePage(t, env, ConsumeOptions{Partition: 0, From: FromOffset, Offset: 0, Limit: 3})
	require.Len(t, res.Messages, 3)
	assert.Equal(t, []int64{4, 5, 6}, []int64{res.Messages[0].Offset, res.Messages[1].Offset, res.Messages[2].Offset})
}

