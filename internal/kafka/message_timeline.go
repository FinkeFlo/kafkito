// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// MaxTimelineSlots caps the number of time slots returned by MessageTimeline
// to bound the number of admin round-trips (one ListOffsetsAfterMilli call
// per time-slot edge).
const MaxTimelineSlots = 400

// TimelineSlot is the approximate message count for one time slot.
type TimelineSlot struct {
	FromTSMs    int64 `json:"from_ts_ms"`
	ToTSMs      int64 `json:"to_ts_ms"`
	ApproxCount int64 `json:"approx_count"`
}

// MessageTimelineOptions configures MessageTimeline.
type MessageTimelineOptions struct {
	Partition int32         // -1 = all partitions
	FromTSMs  int64         // required, inclusive lower bound
	ToTSMs    int64         // required, exclusive upper bound
	SlotMs    int64         // required, slot width in milliseconds
	Timeout   time.Duration // admin-call budget
}

// MessageTimelineResult is the time-sliced message-count series returned by
// MessageTimeline.
type MessageTimelineResult struct {
	FromTSMs int64          `json:"from_ts_ms"`
	ToTSMs   int64          `json:"to_ts_ms"`
	SlotMs   int64          `json:"slot_ms"`
	Slots    []TimelineSlot `json:"slots"`
}

// MessageTimeline resolves a time range into fixed-width time slots and returns
// the approximate number of messages produced inside each slot, without
// consuming any records. It works like CountMessages but samples offsets at
// every time-slot edge instead of just the two range bounds.
func (r *Registry) MessageTimeline(ctx context.Context, cluster, topic string, opts MessageTimelineOptions) (*MessageTimelineResult, error) {
	adm, err := r.Admin(cluster)
	if err != nil {
		return nil, err
	}
	return messageTimelineWithAdmin(ctx, adm, cluster, topic, opts)
}

func messageTimelineWithAdmin(
	ctx context.Context,
	adm messageCountAdmin,
	cluster, topic string,
	opts MessageTimelineOptions,
) (*MessageTimelineResult, error) {
	if opts.FromTSMs < 0 || opts.ToTSMs <= 0 || opts.ToTSMs <= opts.FromTSMs {
		return nil, fmt.Errorf("invalid time range: from_ts_ms must be non-negative and less than to_ts_ms")
	}
	if opts.SlotMs <= 0 {
		return nil, fmt.Errorf("slot_ms must be greater than 0")
	}

	numSlots := int((opts.ToTSMs - opts.FromTSMs + opts.SlotMs - 1) / opts.SlotMs)
	if numSlots < 1 {
		numSlots = 1
	}
	if numSlots > MaxTimelineSlots {
		return nil, fmt.Errorf("requested range/slot combination yields %d slots, exceeding the limit of %d; choose a wider slot or a narrower range", numSlots, MaxTimelineSlots)
	}

	if opts.Timeout <= 0 {
		opts.Timeout = 20 * time.Second
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

	edges := make([]int64, numSlots+1)
	for i := 0; i <= numSlots; i++ {
		ts := opts.FromTSMs + int64(i)*opts.SlotMs
		if ts > opts.ToTSMs {
			ts = opts.ToTSMs
		}
		edges[i] = ts
	}

	offsetAt := make([]map[int32]int64, len(edges))
	for i, ts := range edges {
		listed, err := adm.ListOffsetsAfterMilli(admCtx, ts, topic)
		if err != nil {
			return nil, fmt.Errorf("resolve offsets at ts=%d for topic %q on cluster %q: %w", ts, topic, cluster, err)
		}
		m := make(map[int32]int64, len(parts))
		for _, p := range parts {
			if o, ok := listed.Lookup(topic, p); ok {
				m[p] = o.Offset
			} else {
				// Absent means no record was produced at-or-after ts on this
				// partition, i.e. ts is past the high-watermark.
				m[p] = endMap[p]
			}
			if m[p] < startMap[p] {
				m[p] = startMap[p]
			}
		}
		offsetAt[i] = m
	}

	out := &MessageTimelineResult{
		FromTSMs: opts.FromTSMs,
		ToTSMs:   opts.ToTSMs,
		SlotMs:   opts.SlotMs,
		Slots:    make([]TimelineSlot, 0, numSlots),
	}
	for i := 0; i < numSlots; i++ {
		var total int64
		for _, p := range parts {
			delta := offsetAt[i+1][p] - offsetAt[i][p]
			if delta > 0 {
				total += delta
			}
		}
		out.Slots = append(out.Slots, TimelineSlot{
			FromTSMs:    edges[i],
			ToTSMs:      edges[i+1],
			ApproxCount: total,
		})
	}
	return out, nil
}
