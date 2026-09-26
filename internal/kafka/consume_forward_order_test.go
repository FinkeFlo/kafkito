// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"fmt"
	"math/rand/v2"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// everyFetchLags holds every fetch of the lagging broker for 300ms. The
// other broker answers at once, so a poll returns its records alone first.
func everyFetchLags(int) time.Duration { return 300 * time.Millisecond }

// newLaggingPartitionEnv starts a two-broker kfake cluster with a
// two-partition topic whose partitions have different leaders. The leader of
// partition lagging holds its n-th fetch (counting from 0) for delay(n).
func newLaggingPartitionEnv(t *testing.T, topic string, lagging int32, delay func(n int) time.Duration) *kfakeEnv {
	t.Helper()
	c, err := kfake.NewCluster(kfake.NumBrokers(2), kfake.SeedTopics(2, topic))
	require.NoError(t, err)
	t.Cleanup(c.Close)

	slowNode := c.LeaderFor(topic, lagging)
	other := 1 - lagging
	if c.LeaderFor(topic, other) == slowNode {
		require.NoError(t, c.MoveTopicPartition(topic, other, 1-slowNode))
	}
	require.NotEqual(t, slowNode, c.LeaderFor(topic, other))

	var fetches atomic.Int64
	c.ControlKey(int16(kmsg.Fetch), func(kmsg.Request) (kmsg.Response, error, bool) {
		c.KeepControl()
		if c.CurrentNode() == slowNode {
			if d := delay(int(fetches.Add(1) - 1)); d > 0 {
				c.SleepControl(func() { time.Sleep(d) })
			}
		}
		return nil, nil, false
	})
	return kfakeEnvFor(t, c, topic, nil)
}

// produceSeq writes one record per entry of partitions: record i goes to
// partitions[i] with timestamp fixtureBaseTS+i s and value {"seq":i}.
func (e *kfakeEnv) produceSeq(t *testing.T, partitions []int32) {
	t.Helper()
	for i, p := range partitions {
		e.produce(t, &kgo.Record{
			Partition: p,
			Timestamp: time.UnixMilli(fixtureBaseTS + int64(i)*1000),
			Value:     fmt.Appendf(nil, `{"seq":%d}`, i),
		})
	}
}

// alternating returns n partitions alternating between 0 and 1.
func alternating(n int) []int32 {
	out := make([]int32, n)
	for i := range out {
		out[i] = int32(i % 2)
	}
	return out
}

// orderPatterns are the partition sequences of the lag tests: records
// interleaved in time, and a stretch where partition 0 holds several pages
// of records in a row.
var orderPatterns = map[string][]int32{
	"alternating": alternating(20),
	"bursts":      append(append(alternating(6), make([]int32, 14)...), alternating(6)...),
}

// Forward pages used to be cut as soon as the page size was reached. When a
// poll returned only one partition's records, the page held that partition
// alone and the lagging partition's older records came pages later.
func TestConsume_ForwardPagesKeepOrderWhenAPartitionLags(t *testing.T) {
	t.Parallel()
	for name, pattern := range orderPatterns {
		for _, lagging := range []int32{0, 1} {
			t.Run(fmt.Sprintf("%s/partition %d lags", name, lagging), func(t *testing.T) {
				t.Parallel()
				env := newLaggingPartitionEnv(t, "forward-lag", lagging, everyFetchLags)
				env.produceSeq(t, pattern)

				pages := consumeAllPages(t, env, ConsumeOptions{Partition: -1, Limit: 4, From: FromStart})

				assert.Equal(t, chunk(seqRange(0, len(pattern)-1), 4), pageSeqs(t, pages))
			})
		}
	}
}

func TestConsume_BackwardPagesKeepOrderWhenAPartitionLags(t *testing.T) {
	t.Parallel()
	for name, pattern := range orderPatterns {
		for _, lagging := range []int32{0, 1} {
			t.Run(fmt.Sprintf("%s/partition %d lags", name, lagging), func(t *testing.T) {
				t.Parallel()
				env := newLaggingPartitionEnv(t, "backward-lag", lagging, everyFetchLags)
				env.produceSeq(t, pattern)

				pages := consumeAllPages(t, env, ConsumeOptions{Partition: -1, Limit: 4, From: FromEnd})

				assert.Equal(t, chunk(seqRange(len(pattern)-1, 0), 4), pageSeqs(t, pages))
			})
		}
	}
}

// A forward page waits for every partition that could still contribute. A
// partition whose range ends in a transaction marker must count as drained
// once the marker is read. Otherwise the page would wait for the rest of the
// other partitions' ranges: here partition 1 holds more than one fetch of
// data and its broker holds every fetch after the first beyond the timeout.
func TestConsume_ForwardPageDoesNotWaitForAPartitionEndingInAMarker(t *testing.T) {
	t.Parallel()
	env := newLaggingPartitionEnv(t, "forward-marker", 1, func(n int) time.Duration {
		if n == 0 {
			return 0
		}
		return 20 * time.Second
	})
	env.produceTransactional(t,
		&kgo.Record{Partition: 0, Timestamp: time.UnixMilli(fixtureBaseTS), Key: []byte("s-0")},
		&kgo.Record{Partition: 0, Timestamp: time.UnixMilli(fixtureBaseTS + 1000), Key: []byte("s-1")},
	)
	// Incompressible, so that one fetch cannot carry two of these records.
	big := make([]byte, 700<<10)
	_, _ = rand.NewChaCha8([32]byte{}).Read(big)
	for i := 2; i < 5; i++ {
		env.produce(t, &kgo.Record{
			Partition: 1,
			Timestamp: time.UnixMilli(fixtureBaseTS + int64(i)*1000),
			Key:       fmt.Appendf(nil, "s-%d", i),
			Value:     big,
		})
	}

	start := time.Now()
	res := consumePage(t, env, ConsumeOptions{Partition: -1, Limit: 3, From: FromStart, Timeout: 10 * time.Second})

	assert.Less(t, time.Since(start), 5*time.Second, "waited for the timeout")
	keys := make([]string, 0, len(res.Messages))
	for _, m := range res.Messages {
		keys = append(keys, m.Key)
	}
	assert.Equal(t, []string{"s-0", "s-1", "s-2"}, keys)
}
