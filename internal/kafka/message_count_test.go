// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
)

type fakeMessageCountAdmin struct {
	md             kadm.Metadata
	mdErr          error
	starts         kadm.ListedOffsets
	startsErr      error
	ends           kadm.ListedOffsets
	endsErr        error
	offsetsAfterMS map[int64]kadm.ListedOffsets
	offsetsErr     map[int64]error
}

func (f *fakeMessageCountAdmin) Metadata(_ context.Context, _ ...string) (kadm.Metadata, error) {
	return f.md, f.mdErr
}

func (f *fakeMessageCountAdmin) ListStartOffsets(_ context.Context, _ ...string) (kadm.ListedOffsets, error) {
	return f.starts, f.startsErr
}

func (f *fakeMessageCountAdmin) ListEndOffsets(_ context.Context, _ ...string) (kadm.ListedOffsets, error) {
	return f.ends, f.endsErr
}

func (f *fakeMessageCountAdmin) ListOffsetsAfterMilli(_ context.Context, ms int64, _ ...string) (kadm.ListedOffsets, error) {
	if err := f.offsetsErr[ms]; err != nil {
		return nil, err
	}
	return f.offsetsAfterMS[ms], nil
}

func testTopicMetadata(topic string, partitions ...int32) kadm.Metadata {
	partitionsByID := make(kadm.PartitionDetails, len(partitions))
	for _, p := range partitions {
		partitionsByID[p] = kadm.PartitionDetail{Topic: topic, Partition: p}
	}
	return kadm.Metadata{
		Topics: kadm.TopicDetails{
			topic: {
				Topic:      topic,
				Partitions: partitionsByID,
			},
		},
	}
}

func TestCountMessagesWithAdmin_AllPartitions(t *testing.T) {
	t.Parallel()

	adm := &fakeMessageCountAdmin{
		md: testTopicMetadata("orders", 0, 1),
		starts: kadm.ListedOffsets{
			"orders": {
				0: {Topic: "orders", Partition: 0, Offset: 10},
				1: {Topic: "orders", Partition: 1, Offset: 20},
			},
		},
		ends: kadm.ListedOffsets{
			"orders": {
				0: {Topic: "orders", Partition: 0, Offset: 110},
				1: {Topic: "orders", Partition: 1, Offset: 220},
			},
		},
	}

	got, err := countMessagesWithAdmin(context.Background(), adm, "c1", "orders", CountMessagesOptions{
		Partition: -1,
	})

	require.NoError(t, err)
	require.Nil(t, got.FromTSMs)
	require.Nil(t, got.ToTSMs)
	assert.Equal(t, int64(300), got.TotalApproxCount)
	assert.Equal(t, []PartitionMessageCount{
		{Partition: 0, FromOffset: 10, ToOffset: 110, ApproxCount: 100},
		{Partition: 1, FromOffset: 20, ToOffset: 220, ApproxCount: 200},
	}, got.Partitions)
}

func TestCountMessagesWithAdmin_TimeBoundsCollapseEmptyPartitionsToZero(t *testing.T) {
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
			100: {
				"orders": {
					0: {Topic: "orders", Partition: 0, Offset: 100},
					1: {Topic: "orders", Partition: 1, Offset: 300},
				},
			},
			500: {
				"orders": {
					0: {Topic: "orders", Partition: 0, Offset: 700},
					1: {Topic: "orders", Partition: 1, Offset: 250},
				},
			},
		},
		offsetsErr: map[int64]error{},
	}

	got, err := countMessagesWithAdmin(context.Background(), adm, "c1", "orders", CountMessagesOptions{
		Partition: -1,
		FromTSMs:  100,
		ToTSMs:    500,
	})

	require.NoError(t, err)
	require.NotNil(t, got.FromTSMs)
	require.NotNil(t, got.ToTSMs)
	assert.Equal(t, int64(100), *got.FromTSMs)
	assert.Equal(t, int64(500), *got.ToTSMs)
	assert.Equal(t, int64(600), got.TotalApproxCount)
	assert.Equal(t, []PartitionMessageCount{
		{Partition: 0, FromOffset: 100, ToOffset: 700, ApproxCount: 600},
		{Partition: 1, FromOffset: 300, ToOffset: 300, ApproxCount: 0},
	}, got.Partitions)
}

func TestCountMessagesWithAdmin_RequestedPartitionMustExist(t *testing.T) {
	t.Parallel()

	adm := &fakeMessageCountAdmin{md: testTopicMetadata("orders", 0)}

	_, err := countMessagesWithAdmin(context.Background(), adm, "c1", "orders", CountMessagesOptions{
		Partition: 3,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), `partition 3 not found`)
}

func TestCountMessagesWithAdmin_PropagatesTimestampLookupError(t *testing.T) {
	t.Parallel()

	adm := &fakeMessageCountAdmin{
		md:         testTopicMetadata("orders", 0),
		starts:     kadm.ListedOffsets{"orders": {0: {Topic: "orders", Partition: 0, Offset: 0}}},
		ends:       kadm.ListedOffsets{"orders": {0: {Topic: "orders", Partition: 0, Offset: 10}}},
		offsetsErr: map[int64]error{123: errors.New("boom")},
	}

	_, err := countMessagesWithAdmin(context.Background(), adm, "c1", "orders", CountMessagesOptions{
		Partition: -1,
		FromTSMs:  123,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), `resolve time range`)
	assert.Contains(t, err.Error(), "boom")
}
