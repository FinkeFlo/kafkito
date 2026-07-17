// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
)

func TestMessageTimelineWithAdmin_BucketsSinglePartition(t *testing.T) {
	t.Parallel()

	adm := &fakeMessageCountAdmin{
		md: testTopicMetadata("orders", 0),
		starts: kadm.ListedOffsets{
			"orders": {0: {Topic: "orders", Partition: 0, Offset: 0}},
		},
		ends: kadm.ListedOffsets{
			"orders": {0: {Topic: "orders", Partition: 0, Offset: 1000}},
		},
		offsetsAfterMS: map[int64]kadm.ListedOffsets{
			// 3 buckets of width 100, edges at 0,100,200,300.
			0:   {"orders": {0: {Topic: "orders", Partition: 0, Offset: 0}}},
			100: {"orders": {0: {Topic: "orders", Partition: 0, Offset: 40}}},
			200: {"orders": {0: {Topic: "orders", Partition: 0, Offset: 90}}},
			300: {"orders": {0: {Topic: "orders", Partition: 0, Offset: 90}}},
		},
	}

	got, err := messageTimelineWithAdmin(context.Background(), adm, "c1", "orders", MessageTimelineOptions{
		Partition: -1,
		FromTSMs:  0,
		ToTSMs:    300,
		BucketMs:  100,
	})

	require.NoError(t, err)
	assert.Equal(t, int64(0), got.FromTSMs)
	assert.Equal(t, int64(300), got.ToTSMs)
	assert.Equal(t, int64(100), got.BucketMs)
	assert.Equal(t, []TimelineBucket{
		{FromTSMs: 0, ToTSMs: 100, ApproxCount: 40},
		{FromTSMs: 100, ToTSMs: 200, ApproxCount: 50},
		{FromTSMs: 200, ToTSMs: 300, ApproxCount: 0},
	}, got.Buckets)
}

func TestMessageTimelineWithAdmin_SumsAcrossPartitions(t *testing.T) {
	t.Parallel()

	adm := &fakeMessageCountAdmin{
		md: testTopicMetadata("orders", 0, 1),
		starts: kadm.ListedOffsets{
			"orders": {
				0: {Topic: "orders", Partition: 0, Offset: 0},
				1: {Topic: "orders", Partition: 1, Offset: 0},
			},
		},
		ends: kadm.ListedOffsets{
			"orders": {
				0: {Topic: "orders", Partition: 0, Offset: 1000},
				1: {Topic: "orders", Partition: 1, Offset: 1000},
			},
		},
		offsetsAfterMS: map[int64]kadm.ListedOffsets{
			0: {"orders": {
				0: {Topic: "orders", Partition: 0, Offset: 0},
				1: {Topic: "orders", Partition: 1, Offset: 0},
			}},
			100: {"orders": {
				0: {Topic: "orders", Partition: 0, Offset: 10},
				1: {Topic: "orders", Partition: 1, Offset: 20},
			}},
		},
	}

	got, err := messageTimelineWithAdmin(context.Background(), adm, "c1", "orders", MessageTimelineOptions{
		Partition: -1,
		FromTSMs:  0,
		ToTSMs:    100,
		BucketMs:  100,
	})

	require.NoError(t, err)
	require.Len(t, got.Buckets, 1)
	assert.Equal(t, int64(30), got.Buckets[0].ApproxCount)
}

func TestMessageTimelineWithAdmin_MissingEdgeOffsetFallsBackToEndOffset(t *testing.T) {
	t.Parallel()

	// ts=200 is past the high-watermark, so ListOffsetsAfterMilli returns no
	// entry for that partition; the bucket should fall back to the end
	// offset (nothing produced after the last bucket).
	adm := &fakeMessageCountAdmin{
		md: testTopicMetadata("orders", 0),
		starts: kadm.ListedOffsets{
			"orders": {0: {Topic: "orders", Partition: 0, Offset: 0}},
		},
		ends: kadm.ListedOffsets{
			"orders": {0: {Topic: "orders", Partition: 0, Offset: 50}},
		},
		offsetsAfterMS: map[int64]kadm.ListedOffsets{
			0:   {"orders": {0: {Topic: "orders", Partition: 0, Offset: 0}}},
			100: {"orders": {0: {Topic: "orders", Partition: 0, Offset: 50}}},
			200: {}, // absent => fall back to end offset (50)
		},
	}

	got, err := messageTimelineWithAdmin(context.Background(), adm, "c1", "orders", MessageTimelineOptions{
		Partition: -1,
		FromTSMs:  0,
		ToTSMs:    200,
		BucketMs:  100,
	})

	require.NoError(t, err)
	require.Len(t, got.Buckets, 2)
	assert.Equal(t, int64(50), got.Buckets[0].ApproxCount)
	assert.Equal(t, int64(0), got.Buckets[1].ApproxCount)
}

func TestMessageTimelineWithAdmin_RejectsInvalidRange(t *testing.T) {
	t.Parallel()

	adm := &fakeMessageCountAdmin{md: testTopicMetadata("orders", 0)}

	_, err := messageTimelineWithAdmin(context.Background(), adm, "c1", "orders", MessageTimelineOptions{
		FromTSMs: 100,
		ToTSMs:   50,
		BucketMs: 10,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid time range")
}

func TestMessageTimelineWithAdmin_RejectsTooManyBuckets(t *testing.T) {
	t.Parallel()

	adm := &fakeMessageCountAdmin{md: testTopicMetadata("orders", 0)}

	_, err := messageTimelineWithAdmin(context.Background(), adm, "c1", "orders", MessageTimelineOptions{
		FromTSMs: 0,
		ToTSMs:   int64(MaxTimelineBuckets+1) * 10,
		BucketMs: 10,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeding the limit")
}

func TestMessageTimelineWithAdmin_RequestedPartitionMustExist(t *testing.T) {
	t.Parallel()

	adm := &fakeMessageCountAdmin{md: testTopicMetadata("orders", 0)}

	_, err := messageTimelineWithAdmin(context.Background(), adm, "c1", "orders", MessageTimelineOptions{
		Partition: 3,
		FromTSMs:  0,
		ToTSMs:    100,
		BucketMs:  10,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "partition 3 not found")
}
