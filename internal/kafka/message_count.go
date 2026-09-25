// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
)

// CountMessagesOptions configures CountMessages.
type CountMessagesOptions struct {
	Partition int32         // -1 = all partitions
	FromTSMs  int64         // 0 = unbounded lower timestamp
	ToTSMs    int64         // 0 = unbounded upper timestamp
	Timeout   time.Duration // admin-call budget
}

// PartitionMessageCount is the approximate message count for one partition.
type PartitionMessageCount struct {
	Partition   int32 `json:"partition"`
	FromOffset  int64 `json:"from_offset"`
	ToOffset    int64 `json:"to_offset"`
	ApproxCount int64 `json:"approx_count"`
}

// MessageCountResult is the range-count preview returned by CountMessages.
type MessageCountResult struct {
	FromTSMs         *int64                  `json:"from_ts_ms,omitempty"`
	ToTSMs           *int64                  `json:"to_ts_ms,omitempty"`
	TotalApproxCount int64                   `json:"total_approx_count"`
	Partitions       []PartitionMessageCount `json:"partitions"`
}

type messageCountAdmin interface {
	Metadata(ctx context.Context, topics ...string) (kadm.Metadata, error)
	ListStartOffsets(ctx context.Context, topics ...string) (kadm.ListedOffsets, error)
	ListEndOffsets(ctx context.Context, topics ...string) (kadm.ListedOffsets, error)
	ListOffsetsAfterMilli(ctx context.Context, millis int64, topics ...string) (kadm.ListedOffsets, error)
}

// CountMessages resolves the selected time bounds to offset deltas and returns
// the approximate number of messages inside that range without consuming any
// records.
func (r *Registry) CountMessages(ctx context.Context, cluster, topic string, opts CountMessagesOptions) (*MessageCountResult, error) {
	adm, err := r.Admin(cluster)
	if err != nil {
		return nil, err
	}
	return countMessagesWithAdmin(ctx, adm, cluster, topic, opts)
}

func countMessagesWithAdmin(
	ctx context.Context,
	adm messageCountAdmin,
	cluster, topic string,
	opts CountMessagesOptions,
) (*MessageCountResult, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}
	admCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	md, err := adm.Metadata(admCtx, topic)
	if err != nil {
		return nil, fmt.Errorf("fetch metadata for topic %q on cluster %q: %w", topic, cluster, err)
	}
	t, ok := md.Topics[topic]
	if !ok || t.Err != nil {
		return nil, fmt.Errorf("topic %q not found on cluster %q", topic, cluster)
	}

	allParts := make([]int32, 0, len(t.Partitions))
	for _, p := range t.Partitions {
		allParts = append(allParts, p.Partition)
	}
	sort.Slice(allParts, func(i, j int) bool { return allParts[i] < allParts[j] })

	parts := allParts
	if opts.Partition >= 0 {
		if !containsPartition(allParts, opts.Partition) {
			return nil, fmt.Errorf("partition %d not found in topic %q on cluster %q", opts.Partition, topic, cluster)
		}
		parts = []int32{opts.Partition}
	}

	starts, err := adm.ListStartOffsets(admCtx, topic)
	if err != nil {
		return nil, fmt.Errorf("list start offsets for topic %q on cluster %q: %w", topic, cluster, err)
	}
	ends, err := adm.ListEndOffsets(admCtx, topic)
	if err != nil {
		return nil, fmt.Errorf("list end offsets for topic %q on cluster %q: %w", topic, cluster, err)
	}

	startMap := make(map[int32]int64, len(parts))
	endMap := make(map[int32]int64, len(parts))
	for _, p := range parts {
		if so, ok := starts.Lookup(topic, p); ok {
			startMap[p] = so.Offset
		}
		if eo, ok := ends.Lookup(topic, p); ok {
			endMap[p] = eo.Offset
		}
	}

	var fromOffsets, toOffsets map[int32]int64
	if opts.FromTSMs > 0 || opts.ToTSMs > 0 {
		fromOffsets, toOffsets, err = resolveTimestampOffsets(admCtx, adm, topic, parts, opts.FromTSMs, opts.ToTSMs)
		if err != nil {
			return nil, fmt.Errorf("resolve time range for topic %q on cluster %q: %w", topic, cluster, err)
		}
	}

	out := &MessageCountResult{
		TotalApproxCount: 0,
		Partitions:       make([]PartitionMessageCount, 0, len(parts)),
	}
	if opts.FromTSMs > 0 {
		out.FromTSMs = ptrInt64(opts.FromTSMs)
	}
	if opts.ToTSMs > 0 {
		out.ToTSMs = ptrInt64(opts.ToTSMs)
	}

	for _, p := range parts {
		fromOffset, toOffset := clampCountRange(p, opts, startMap, endMap, fromOffsets, toOffsets)
		approx := toOffset - fromOffset
		if approx < 0 {
			approx = 0
			toOffset = fromOffset
		}
		out.TotalApproxCount += approx
		out.Partitions = append(out.Partitions, PartitionMessageCount{
			Partition:   p,
			FromOffset:  fromOffset,
			ToOffset:    toOffset,
			ApproxCount: approx,
		})
	}

	return out, nil
}

func clampCountRange(
	partition int32,
	opts CountMessagesOptions,
	startMap, endMap map[int32]int64,
	fromOffsets, toOffsets map[int32]int64,
) (int64, int64) {
	fromOffset := startMap[partition]
	toOffset := endMap[partition]
	if opts.FromTSMs > 0 {
		if o, ok := fromOffsets[partition]; ok && o > fromOffset {
			fromOffset = o
		}
	}
	if opts.ToTSMs > 0 {
		if o, ok := toOffsets[partition]; ok && o < toOffset {
			toOffset = o
		}
	}
	if toOffset < fromOffset {
		toOffset = fromOffset
	}
	return fromOffset, toOffset
}
