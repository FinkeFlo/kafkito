// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestExplicitOrKeyPartitioner_HonoursExplicitPartition(t *testing.T) {
	t.Parallel()

	tp := explicitOrKeyPartitioner().ForTopic("t")

	for _, p := range []int32{0, 1, 3} {
		rec := &kgo.Record{Partition: p, Key: []byte("same-key")}
		assert.Equal(t, int(p), tp.Partition(rec, 4),
			"an explicitly partitioned record must not be re-hashed")
		assert.True(t, tp.RequiresConsistency(rec),
			"the caller asked for this partition specifically")
	}
}

// Partition 0 is the zero value of kgo.Record.Partition, so this case is the
// one that silently "worked" before and would keep working if the sentinel
// were ever dropped: it must be honoured because it was asked for, not because
// it happens to equal the default.
func TestExplicitOrKeyPartitioner_OutOfRangePartitionIsRejected(t *testing.T) {
	t.Parallel()

	tp := explicitOrKeyPartitioner().ForTopic("t")
	rec := &kgo.Record{Partition: 9}

	assert.Negative(t, tp.Partition(rec, 4),
		"an out-of-range partition must fail the record, not silently land elsewhere")
}

func TestExplicitOrKeyPartitioner_FallsBackToKeyHashing(t *testing.T) {
	t.Parallel()

	tp := explicitOrKeyPartitioner().ForTopic("t")
	ref := kgo.StickyKeyPartitioner(nil).ForTopic("t")

	// Same key must map to the same partition as franz-go's default would,
	// and must be stable across calls.
	for _, key := range []string{"a", "b", "order-42", ""} {
		rec := &kgo.Record{Partition: unsetPartition, Key: []byte(key)}
		want := ref.Partition(&kgo.Record{Partition: unsetPartition, Key: []byte(key)}, 4)
		got := tp.Partition(rec, 4)
		assert.Equal(t, want, got, "key %q must hash like the default partitioner", key)
		assert.Equal(t, got, tp.Partition(rec, 4), "hashing must be stable for key %q", key)
	}
}

// A keyless record is handed to a sticky partitioner that only moves off its
// current partition when OnNewBatch fires. If the wrapper swallowed that call,
// every keyless record would pile onto one partition for the client's whole
// lifetime — invisible in a single-record test, and exactly the kind of
// throughput/skew regression nobody notices until a topic is lopsided.
func TestExplicitOrKeyPartitioner_ForwardsOnNewBatch(t *testing.T) {
	t.Parallel()

	tp := explicitOrKeyPartitioner().ForTopic("t")
	nb, ok := tp.(kgo.TopicPartitionerOnNewBatch)
	require.True(t, ok, "wrapper must expose OnNewBatch so kgo calls it")

	const n = 4
	seen := map[int]bool{}
	for i := 0; i < 200; i++ {
		seen[tp.Partition(&kgo.Record{Partition: unsetPartition}, n)] = true
		nb.OnNewBatch()
	}

	assert.Greater(t, len(seen), 1,
		"keyless records must rotate across partitions once batches roll over")
}

func TestExplicitOrKeyPartitioner_KeylessIsStickyWithinABatch(t *testing.T) {
	t.Parallel()

	tp := explicitOrKeyPartitioner().ForTopic("t")

	first := tp.Partition(&kgo.Record{Partition: unsetPartition}, 4)
	for i := 0; i < 20; i++ {
		assert.Equal(t, first, tp.Partition(&kgo.Record{Partition: unsetPartition}, 4),
			"without OnNewBatch the partition must stay put")
	}
}
