// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"fmt"

	"github.com/twmb/franz-go/pkg/kadm"
)

// resolveTimestampOffsets resolves a timestamp-millis lower-bound and
// upper-bound to per-partition offsets via kadm.ListOffsetsAfterMilli.
// Either bound may be 0 (unset). Returns maps keyed by partition; absent
// keys mean "no record produced at-or-after the given timestamp on that
// partition" (i.e., the bound is past the high-watermark).
//
// Used by both ConsumeMessages (for the time-range messages-view feature)
// and SearchMessages (for the same predicate-search feature).
func resolveTimestampOffsets(
	ctx context.Context,
	adm *kadm.Client,
	topic string,
	partitions []int32,
	fromTSMs, toTSMs int64,
) (fromOffsets, toOffsets map[int32]int64, err error) {
	lookup := func(ms int64) (map[int32]int64, error) {
		lo, err := adm.ListOffsetsAfterMilli(ctx, ms, topic)
		if err != nil {
			return nil, fmt.Errorf("list offsets after ts=%d for topic %q: %w", ms, topic, err)
		}
		m := make(map[int32]int64, len(partitions))
		for _, p := range partitions {
			if po, ok := lo.Lookup(topic, p); ok {
				m[p] = po.Offset
			}
		}
		return m, nil
	}
	if fromTSMs > 0 {
		fromOffsets, err = lookup(fromTSMs)
		if err != nil {
			return nil, nil, err
		}
	}
	if toTSMs > 0 {
		toOffsets, err = lookup(toTSMs)
		if err != nil {
			return nil, nil, err
		}
	}
	return fromOffsets, toOffsets, nil
}
