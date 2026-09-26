// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
)

// The tests in this file pin CountMessages and MessageTimeline against an
// in-memory cluster and check that the record readers release their
// short-lived consumer clients.

func TestCountCharacterization_AllPartitionsAndTimeRange(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)
	ctx := context.Background()

	all, err := env.reg.CountMessages(ctx, kfakeCluster, env.topic, CountMessagesOptions{Partition: -1})
	require.NoError(t, err)
	assert.Equal(t, &MessageCountResult{
		TotalApproxCount: 24,
		Partitions: []PartitionMessageCount{
			{Partition: 0, FromOffset: 0, ToOffset: 12, ApproxCount: 12},
			{Partition: 1, FromOffset: 0, ToOffset: 8, ApproxCount: 8},
			{Partition: 2, FromOffset: 0, ToOffset: 4, ApproxCount: 4},
		},
	}, all)

	from, to := fixtureBaseTS+5_000, fixtureBaseTS+15_000
	ranged, err := env.reg.CountMessages(ctx, kfakeCluster, env.topic, CountMessagesOptions{Partition: -1, FromTSMs: from, ToTSMs: to})
	require.NoError(t, err)
	assert.Equal(t, &MessageCountResult{
		FromTSMs:         &from,
		ToTSMs:           &to,
		TotalApproxCount: 10,
		Partitions: []PartitionMessageCount{
			{Partition: 0, FromOffset: 3, ToOffset: 8, ApproxCount: 5},
			{Partition: 1, FromOffset: 1, ToOffset: 5, ApproxCount: 4},
			{Partition: 2, FromOffset: 1, ToOffset: 2, ApproxCount: 1},
		},
	}, ranged)

	one, err := env.reg.CountMessages(ctx, kfakeCluster, env.topic, CountMessagesOptions{Partition: 1})
	require.NoError(t, err)
	assert.Equal(t, int64(8), one.TotalApproxCount)
	assert.Len(t, one.Partitions, 1)

	_, err = env.reg.CountMessages(ctx, kfakeCluster, env.topic, CountMessagesOptions{Partition: 7})
	require.EqualError(t, err, `partition 7 not found in topic "orders" on cluster "kf"`)
	_, err = env.reg.CountMessages(ctx, kfakeCluster, "missing", CountMessagesOptions{Partition: -1})
	require.Error(t, err)
}

func TestTimelineCharacterization_Slots(t *testing.T) {
	t.Parallel()
	env, _ := newOrdersEnv(t)
	ctx := context.Background()

	all, err := env.reg.MessageTimeline(ctx, kfakeCluster, env.topic, MessageTimelineOptions{
		Partition: -1, FromTSMs: fixtureBaseTS, ToTSMs: fixtureBaseTS + 24_000, SlotMs: 6_000,
	})
	require.NoError(t, err)
	want := make([]TimelineSlot, 0, 4)
	for i := range int64(4) {
		want = append(want, TimelineSlot{FromTSMs: fixtureBaseTS + i*6_000, ToTSMs: fixtureBaseTS + (i+1)*6_000, ApproxCount: 6})
	}
	assert.Equal(t, &MessageTimelineResult{FromTSMs: fixtureBaseTS, ToTSMs: fixtureBaseTS + 24_000, SlotMs: 6_000, Slots: want}, all)

	p0, err := env.reg.MessageTimeline(ctx, kfakeCluster, env.topic, MessageTimelineOptions{
		Partition: 0, FromTSMs: fixtureBaseTS + 3_000, ToTSMs: fixtureBaseTS + 30_000, SlotMs: 10_000,
	})
	require.NoError(t, err)
	// p0 holds the even seqs: 4..12 in slot one, 14..22 in slot two, none after.
	counts := []int64{}
	for _, s := range p0.Slots {
		counts = append(counts, s.ApproxCount)
	}
	assert.Equal(t, []int64{5, 5, 0}, counts)
	assert.Equal(t, fixtureBaseTS+30_000, p0.Slots[2].ToTSMs)

	_, err = env.reg.MessageTimeline(ctx, kfakeCluster, env.topic, MessageTimelineOptions{
		Partition: 5, FromTSMs: fixtureBaseTS, ToTSMs: fixtureBaseTS + 1, SlotMs: 1,
	})
	require.EqualError(t, err, `partition 5 not found in topic "orders" on cluster "kf"`)
}

func TestRawValueCharacterization(t *testing.T) {
	t.Parallel()
	env, fx := newOrdersEnv(t)
	ctx := context.Background()

	raw, err := env.reg.FetchRawMessageValue(ctx, kfakeCluster, env.topic, 0, 3)
	require.NoError(t, err)
	assert.JSONEq(t, `{"seq":6,"kind":"even","name":"rec-6"}`, string(raw.Value))
	assert.Equal(t, "application/json", raw.ContentType)
	assert.Equal(t, "json", raw.Extension)
	assert.Equal(t, fixtureRecord{Seq: 6, Partition: 0, Offset: 3, Timestamp: fixtureBaseTS + 6_000}, fx[6])

	bin := newKfakeEnv(t, "bin", 1, nil)
	bin.produce(t, &kgo.Record{Value: []byte{0xff, 0x00, 0xfe}}, &kgo.Record{Value: []byte("plain")})
	raw, err = bin.reg.FetchRawMessageValue(ctx, kfakeCluster, "bin", 0, 0)
	require.NoError(t, err)
	assert.Equal(t, &RawMessageValue{Value: []byte{0xff, 0x00, 0xfe}, ContentType: "application/octet-stream", Extension: "bin"}, raw)
	raw, err = bin.reg.FetchRawMessageValue(ctx, kfakeCluster, "bin", 0, 1)
	require.NoError(t, err)
	assert.Equal(t, "text/plain; charset=utf-8", raw.ContentType)

	short, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	_, err = env.reg.FetchRawMessageValue(short, kfakeCluster, env.topic, 2, 100)
	require.Error(t, err, "an offset past the end waits for the context and fails")

	_, err = env.reg.FetchRawMessageValue(ctx, "nope", env.topic, 0, 0)
	require.ErrorIs(t, err, ErrUnknownCluster)
}

// TestRecordReaders_CloseTheirClients runs every record reader repeatedly and
// checks that the goroutine count returns to its baseline, i.e. that each
// short-lived consumer client is closed again. It is deliberately not
// parallel so other tests do not disturb the count.
func TestRecordReaders_CloseTheirClients(t *testing.T) {
	env, _ := newOrdersEnv(t)
	ctx := context.Background()
	readAll := func() {
		consumePage(t, env, ConsumeOptions{Partition: -1, Limit: 5, From: FromEnd})
		consumePage(t, env, ConsumeOptions{Partition: -1, Limit: 5, From: FromStart})
		searchTopic(t, env, SearchOptions{Partition: -1, Limit: 500, Value: "rec"})
		searchTopic(t, env, SearchOptions{Partition: -1, Limit: 500, Direction: DirOldestFirst, Value: "rec"})
		_, err := env.reg.FetchRawMessageValue(ctx, kfakeCluster, env.topic, 0, 3)
		require.NoError(t, err)
		_, err = env.reg.CountMessages(ctx, kfakeCluster, env.topic, CountMessagesOptions{Partition: -1})
		require.NoError(t, err)
	}

	readAll() // warm up the cached admin client
	time.Sleep(200 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	for range 5 {
		readAll()
	}

	assert.Eventually(t, func() bool { return runtime.NumGoroutine() <= baseline+2 }, 10*time.Second, 50*time.Millisecond,
		"goroutines did not return to baseline %d (now %d): a consumer client leaked", baseline, runtime.NumGoroutine())
}
