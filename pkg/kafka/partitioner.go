// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import "github.com/twmb/franz-go/pkg/kgo"

// unsetPartition marks a record as "let the client choose". buildRecord stamps
// it on every record that carries no caller-chosen partition, because a
// kgo.Record's zero value for Partition is 0 — a perfectly valid partition —
// so "unset" is otherwise indistinguishable from "partition 0".
const unsetPartition int32 = -1

// explicitOrKeyPartitioner routes a record to the partition the caller put on
// it (Partition >= 0) and otherwise falls back to franz-go's default
// key-sticky behaviour.
//
// Without it, a caller-chosen partition is silently ignored: kgo documents
// Record.Partition as "for producing, this is left unset … this will be set by
// the client", and the default StickyKeyPartitioner overwrites whatever the
// caller wrote there. That made both the produce endpoint's `partition`
// parameter and the topic-copy `preserve_partition` option no-ops — records
// were acknowledged and landed on a hash-chosen partition instead, with
// nothing anywhere reporting that the request had been disregarded.
//
// ManualPartitioner is not an option here: the same client serves every
// produce for a cluster, and it would pin all the records that legitimately
// have no partition (the common case) to partition 0.
func explicitOrKeyPartitioner() kgo.Partitioner {
	return explicitPartitioner{fallback: kgo.StickyKeyPartitioner(nil)}
}

type explicitPartitioner struct{ fallback kgo.Partitioner }

func (p explicitPartitioner) ForTopic(topic string) kgo.TopicPartitioner {
	return &explicitTopicPartitioner{fallback: p.fallback.ForTopic(topic)}
}

type explicitTopicPartitioner struct{ fallback kgo.TopicPartitioner }

// RequiresConsistency reports true for an explicitly partitioned record: the
// caller asked for that partition specifically, so the record must wait for it
// rather than be rerouted when the partition is unavailable.
func (p *explicitTopicPartitioner) RequiresConsistency(r *kgo.Record) bool {
	if r.Partition >= 0 {
		return true
	}
	return p.fallback.RequiresConsistency(r)
}

func (p *explicitTopicPartitioner) Partition(r *kgo.Record, n int) int {
	if r.Partition >= 0 {
		if int(r.Partition) < n {
			return int(r.Partition)
		}
		// Out of range: return an invalid index so kgo fails this record
		// instead of quietly writing it somewhere else. Callers that can
		// check up front (see the topic-copy handler's preserve_partition
		// validation) should reject before producing; this is the backstop
		// for a topic that shrank— or rather, for a destination whose
		// partition count we never looked at.
		return -1
	}
	return p.fallback.Partition(r, n)
}

// OnNewBatch forwards to the fallback, which needs it: StickyKeyPartitioner
// delegates keyless records to a sticky partitioner that only rotates away
// from its current partition when a new batch starts. Swallowing this call
// would pin every keyless record to one partition for the client's lifetime.
func (p *explicitTopicPartitioner) OnNewBatch() {
	if nb, ok := p.fallback.(kgo.TopicPartitionerOnNewBatch); ok {
		nb.OnNewBatch()
	}
}
