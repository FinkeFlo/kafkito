// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
)

// topicOffsetsAdmin is the subset of *kadm.Client the offset resolution needs.
// Tests substitute a fake.
type topicOffsetsAdmin interface {
	Metadata(ctx context.Context, topics ...string) (kadm.Metadata, error)
	ListStartOffsets(ctx context.Context, topics ...string) (kadm.ListedOffsets, error)
	ListEndOffsets(ctx context.Context, topics ...string) (kadm.ListedOffsets, error)
	ListOffsetsAfterMilli(ctx context.Context, millis int64, topics ...string) (kadm.ListedOffsets, error)
}

// offsetsQuery selects the partitions and optional time bounds to resolve.
type offsetsQuery struct {
	partition int32 // -1 = all partitions
	// unchecked skips the check that partition exists; an unknown partition
	// then simply has no start/end offsets.
	unchecked        bool
	fromTSMs, toTSMs int64 // 0 = unset
}

// topicOffsets is the admin-side view of a topic every record reader needs
// before it reads a single record.
type topicOffsets struct {
	parts      []int32 // sorted ascending
	start, end map[int32]int64
	// fromTS/toTS hold the first offset at or after the respective time
	// bound, keyed by partition; nil when the bound is unset.
	fromTS, toTS map[int32]int64
}

// readerOffsets loads the offsets a record reader needs, within the admin
// budget of an interactive request.
func (r *Registry) readerOffsets(ctx context.Context, cluster, topic string, q offsetsQuery) (*topicOffsets, error) {
	adm, err := r.Admin(cluster)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return loadTopicOffsets(ctx, adm, cluster, topic, q)
}

// loadTopicOffsets resolves the partitions of topic, their start and end
// offsets and, if requested, the offsets of the time bounds.
func loadTopicOffsets(ctx context.Context, adm topicOffsetsAdmin, cluster, topic string, q offsetsQuery) (*topicOffsets, error) {
	parts, err := selectPartitions(ctx, adm, cluster, topic, q)
	if err != nil {
		return nil, err
	}
	starts, err := adm.ListStartOffsets(ctx, topic)
	if err != nil {
		return nil, fmt.Errorf("list start offsets for topic %q on cluster %q: %w", topic, cluster, err)
	}
	ends, err := adm.ListEndOffsets(ctx, topic)
	if err != nil {
		return nil, fmt.Errorf("list end offsets for topic %q on cluster %q: %w", topic, cluster, err)
	}
	out := &topicOffsets{
		parts: parts,
		start: offsetsOf(starts, topic, parts),
		end:   offsetsOf(ends, topic, parts),
	}
	out.fromTS, out.toTS, err = resolveTimestampOffsets(ctx, adm, topic, parts, q.fromTSMs, q.toTSMs)
	if err != nil {
		return nil, fmt.Errorf("resolve time range for topic %q on cluster %q: %w", topic, cluster, err)
	}
	return out, nil
}

// bounds returns the offset range of partition p narrowed to the time bounds.
func (o *topicOffsets) bounds(p int32) (int64, int64) {
	return clampRange(p, o.start[p], o.end[p], o.fromTS, o.toTS)
}

func selectPartitions(ctx context.Context, adm topicOffsetsAdmin, cluster, topic string, q offsetsQuery) ([]int32, error) {
	md, err := adm.Metadata(ctx, topic)
	if err != nil {
		return nil, fmt.Errorf("fetch metadata for topic %q on cluster %q: %w", topic, cluster, err)
	}
	t, ok := md.Topics[topic]
	if !ok || t.Err != nil {
		return nil, fmt.Errorf("topic %q not found on cluster %q", topic, cluster)
	}
	all := make([]int32, 0, len(t.Partitions))
	for _, p := range t.Partitions {
		all = append(all, p.Partition)
	}
	slices.Sort(all)
	if q.partition < 0 {
		return all, nil
	}
	if !q.unchecked && !slices.Contains(all, q.partition) {
		return nil, fmt.Errorf("partition %d not found in topic %q on cluster %q", q.partition, topic, cluster)
	}
	return []int32{q.partition}, nil
}

func offsetsOf(listed kadm.ListedOffsets, topic string, parts []int32) map[int32]int64 {
	out := make(map[int32]int64, len(parts))
	for _, p := range parts {
		if o, ok := listed.Lookup(topic, p); ok {
			out[p] = o.Offset
		}
	}
	return out
}
