// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	gen "github.com/FinkeFlo/kafkito/internal/server/api"
)

// The request validator enforces the rules api/openapi.yaml states for these
// parameters (types, enums, minimums, required, path maxLength). What is left
// here are defaults, clamping and the cross-field rules the spec cannot
// express. Their error texts are unchanged from the hand-written parsing.

// maxSearchBodyBytes bounds the search request body to protect against memory
// exhaustion, matching the cap used by the other JSON handlers.
const maxSearchBodyBytes = 1 << 20

// maxConsumeLimit caps the number of messages a single consume request may fetch.
const maxConsumeLimit = 500

// maxPartitionOffsetsEntries caps the comma-separated partition_offsets
// query value. A topic with thousands of partitions is exotic; this guard
// stops a malicious or malformed input from forcing quadratic admin work
// in the kafka layer.
const maxPartitionOffsetsEntries = 1024

// maxSampleSize is the upper clamp of the sample endpoint's n.
const maxSampleSize = 25

// consumeOptions maps the consumeMessages parameters to ConsumeOptions.
func consumeOptions(p gen.ConsumeMessagesParams) (kafkapkg.ConsumeOptions, error) {
	opts := kafkapkg.ConsumeOptions{
		Partition: -1,
		Limit:     50,
		From:      kafkapkg.FromEnd,
		Timeout:   6 * time.Second,
	}
	if p.Partition != nil {
		opts.Partition = *p.Partition
	}
	if p.Limit != nil {
		opts.Limit = min(*p.Limit, maxConsumeLimit)
	}
	var rawFrom string
	if p.From != nil {
		rawFrom = string(*p.From)
		opts.From = kafkapkg.ConsumeFrom(rawFrom)
	}
	if p.FromTsMs != nil {
		opts.FromTSMs = *p.FromTsMs
	}
	if p.ToTsMs != nil {
		opts.ToTSMs = *p.ToTsMs
	}
	if opts.FromTSMs > 0 && opts.ToTSMs > 0 && opts.ToTSMs < opts.FromTSMs {
		return opts, badRequest("to_ts_ms must be >= from_ts_ms")
	}

	if p.PartitionOffsets != nil && *p.PartitionOffsets != "" {
		if opts.From != kafkapkg.FromOffset {
			return opts, badRequest("partition_offsets requires from=offset")
		}
		offs, err := parsePartitionOffsets(*p.PartitionOffsets)
		if err != nil {
			return opts, badRequest("invalid partition_offsets: " + err.Error())
		}
		opts.PartitionOffsets = offs
	}

	if opts.From == kafkapkg.FromOffset && len(opts.PartitionOffsets) == 0 {
		if p.Offset == nil {
			return opts, badRequest("invalid offset")
		}
		opts.Offset = *p.Offset
	}

	if p.Cursor != nil && *p.Cursor != "" {
		c, err := kafkapkg.DecodeCursor(*p.Cursor)
		if err != nil {
			return opts, badRequest("invalid cursor: " + err.Error())
		}
		switch c.Direction {
		case kafkapkg.CursorBackward:
			if rawFrom != "" && rawFrom != "end" {
				return opts, badRequest(fmt.Sprintf("cursor direction backward conflicts with from=%s", rawFrom))
			}
			opts.From = kafkapkg.FromEnd
			opts.CursorUpperBounds = c.Partitions
		case kafkapkg.CursorForward:
			if rawFrom == "end" {
				return opts, badRequest("cursor direction forward conflicts with from=end")
			}
			opts.From = kafkapkg.FromOffset
			opts.PartitionOffsets = c.Partitions
		}
	}

	return opts, nil
}

// countOptions maps the countMessages parameters to CountMessagesOptions.
func countOptions(p gen.CountMessagesParams) (kafkapkg.CountMessagesOptions, error) {
	opts := kafkapkg.CountMessagesOptions{
		Partition: -1,
		Timeout:   6 * time.Second,
	}
	if p.Partition != nil {
		opts.Partition = *p.Partition
	}
	if p.FromTsMs != nil {
		opts.FromTSMs = *p.FromTsMs
	}
	if p.ToTsMs != nil {
		opts.ToTSMs = *p.ToTsMs
	}
	if opts.FromTSMs > 0 && opts.ToTSMs > 0 && opts.ToTSMs < opts.FromTSMs {
		return opts, badRequest("to_ts_ms must be >= from_ts_ms")
	}
	return opts, nil
}

// timelineOptions maps the getMessageTimeline parameters to
// MessageTimelineOptions. The spec requires from_ts_ms, to_ts_ms and slot_ms
// and bounds them to >= 1.
func timelineOptions(p gen.GetMessageTimelineParams) (kafkapkg.MessageTimelineOptions, error) {
	opts := kafkapkg.MessageTimelineOptions{
		Partition: -1,
		FromTSMs:  p.FromTsMs,
		ToTSMs:    p.ToTsMs,
		SlotMs:    p.SlotMs,
		Timeout:   20 * time.Second,
	}
	if p.Partition != nil {
		opts.Partition = *p.Partition
	}
	if opts.ToTSMs <= opts.FromTSMs {
		return opts, badRequest("to_ts_ms must be > from_ts_ms")
	}
	numSlots := (opts.ToTSMs - opts.FromTSMs + opts.SlotMs - 1) / opts.SlotMs
	if numSlots > kafkapkg.MaxTimelineSlots {
		return opts, badRequest(fmt.Sprintf("range/slot combination yields %d slots, exceeding the limit of %d", numSlots, kafkapkg.MaxTimelineSlots))
	}
	return opts, nil
}

// parsePartitionOffsets parses a "p:o,p:o,…" list of per-partition seek
// offsets. Used by the GET /messages endpoint when the caller supplies
// from=offset across all partitions without a continuation cursor.
func parsePartitionOffsets(s string) (map[int32]int64, error) {
	pairs := strings.Split(s, ",")
	if len(pairs) > maxPartitionOffsetsEntries {
		return nil, fmt.Errorf("too many entries (max %d)", maxPartitionOffsetsEntries)
	}
	out := make(map[int32]int64, len(pairs))
	for _, raw := range pairs {
		pair := strings.TrimSpace(raw)
		if pair == "" {
			return nil, fmt.Errorf("empty pair")
		}
		kv := strings.SplitN(pair, ":", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid pair %q", pair)
		}
		pi, err := strconv.ParseInt(kv[0], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid partition %q", kv[0])
		}
		oi, err := strconv.ParseInt(kv[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid offset %q", kv[1])
		}
		if oi < 0 {
			return nil, fmt.Errorf("negative offset %d", oi)
		}
		if _, dup := out[int32(pi)]; dup {
			return nil, fmt.Errorf("duplicate partition %d", pi)
		}
		out[int32(pi)] = oi
	}
	return out, nil
}

// sampleOptions maps the sampleMessages parameters to ConsumeOptions.
// Defaults: n=5, partition=-1, from=end. n is clamped to 1..25.
func sampleOptions(p gen.SampleMessagesParams) kafkapkg.ConsumeOptions {
	opts := kafkapkg.ConsumeOptions{
		Partition: -1,
		Limit:     5,
		From:      kafkapkg.FromEnd,
		Timeout:   6 * time.Second,
	}
	if p.Partition != nil {
		opts.Partition = *p.Partition
	}
	if p.N != nil {
		opts.Limit = min(max(*p.N, 1), maxSampleSize)
	}
	return opts
}

// searchOptions maps an optional SearchRequest body to SearchOptions.
func searchOptions(body *gen.SearchRequest) (kafkapkg.SearchOptions, error) {
	opts := kafkapkg.SearchOptions{
		Partition:   -1,
		StopOnLimit: true,
		Timeout:     12 * time.Second,
	}
	if body == nil {
		return opts, nil
	}
	if body.Partition != nil {
		opts.Partition = *body.Partition
	}
	opts.Limit = deref(body.Limit)
	opts.Budget = deref(body.Budget)
	opts.Direction = kafkapkg.SearchDirection(deref(body.Direction))
	opts.Mode = kafkapkg.SearchMode(deref(body.Mode))
	opts.Path = deref(body.Path)
	opts.Op = kafkapkg.SearchOp(deref(body.Op))
	opts.Value = deref(body.Value)
	opts.FromTS = deref(body.FromTsMs)
	opts.ToTS = deref(body.ToTsMs)
	if body.StopOnLimit != nil {
		opts.StopOnLimit = *body.StopOnLimit
	}
	if body.Zones != nil {
		for _, z := range *body.Zones {
			opts.Zones = append(opts.Zones, kafkapkg.SearchZone(z))
		}
	}
	if body.Cursors != nil && len(*body.Cursors) > 0 {
		opts.Cursors = make(map[int32]int64, len(*body.Cursors))
		for k, v := range *body.Cursors {
			pn, err := strconv.ParseInt(k, 10, 32)
			if err != nil {
				return opts, badRequest(fmt.Sprintf("invalid partition key in cursors: %q", k))
			}
			opts.Cursors[int32(pn)] = v
		}
	}
	return opts, nil
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
