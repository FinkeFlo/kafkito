// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"time"
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
	adm topicOffsetsAdmin,
	cluster, topic string,
	opts CountMessagesOptions,
) (*MessageCountResult, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}
	admCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	offs, err := loadTopicOffsets(admCtx, adm, cluster, topic, offsetsQuery{
		partition: opts.Partition, fromTSMs: opts.FromTSMs, toTSMs: opts.ToTSMs,
	})
	if err != nil {
		return nil, err
	}

	out := &MessageCountResult{
		TotalApproxCount: 0,
		Partitions:       make([]PartitionMessageCount, 0, len(offs.parts)),
	}
	if opts.FromTSMs > 0 {
		out.FromTSMs = ptrInt64(opts.FromTSMs)
	}
	if opts.ToTSMs > 0 {
		out.ToTSMs = ptrInt64(opts.ToTSMs)
	}

	for _, p := range offs.parts {
		fromOffset, toOffset := offs.bounds(p)
		toOffset = max(toOffset, fromOffset)
		approx := toOffset - fromOffset
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
