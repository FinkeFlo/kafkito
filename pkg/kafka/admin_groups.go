// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/twmb/franz-go/pkg/kadm"
)

// --- Reset consumer group offsets ---------------------------------------------

// ResetOffsetStrategy selects how new offsets are computed.
//   - earliest: commit the current log-start offset for each selected partition.
//   - latest:   commit the current log-end offset for each selected partition.
//   - offset:   commit a specific absolute offset (per partition or global).
//   - timestamp: resolve the first offset at/after TimestampMs per partition.
//   - shift-by: committed = current-committed + Shift (can be negative).
type ResetOffsetStrategy string

// Reset strategies supported by ResetOffsets.
const (
	ResetEarliest  ResetOffsetStrategy = "earliest"
	ResetLatest    ResetOffsetStrategy = "latest"
	ResetToOffset  ResetOffsetStrategy = "offset"
	ResetTimestamp ResetOffsetStrategy = "timestamp"
	ResetShiftBy   ResetOffsetStrategy = "shift-by"
)

// ResetOffsetsRequest describes a reset for one topic on a single group.
type ResetOffsetsRequest struct {
	Topic       string              `json:"topic"`
	Partitions  []int32             `json:"partitions,omitempty"` // omit / empty = all partitions of Topic
	Strategy    ResetOffsetStrategy `json:"strategy"`
	Offset      int64               `json:"offset,omitempty"`       // when strategy=offset
	TimestampMs int64               `json:"timestamp_ms,omitempty"` // when strategy=timestamp
	Shift       int64               `json:"shift,omitempty"`        // when strategy=shift-by
	DryRun      bool                `json:"dry_run,omitempty"`
}

// ResetOffsetResult is the per-partition outcome of a reset.
type ResetOffsetResult struct {
	Partition int32  `json:"partition"`
	OldOffset int64  `json:"old_offset"` // -1 if no prior commit
	NewOffset int64  `json:"new_offset"`
	Error     string `json:"error,omitempty"`
}

// ResetOffsetsResult bundles the outcomes.
type ResetOffsetsResult struct {
	Group   string              `json:"group"`
	Topic   string              `json:"topic"`
	DryRun  bool                `json:"dry_run"`
	Results []ResetOffsetResult `json:"results"`
}

// ResetOffsets computes and optionally commits new offsets for a consumer group.
// The group must be Empty or Dead; resetting offsets of an active group is
// refused by the broker (GROUP_IS_NOT_EMPTY). We still pass the request through
// so the broker-side error bubbles up cleanly.
func (r *Registry) ResetOffsets(ctx context.Context, cluster, group string, req ResetOffsetsRequest) (*ResetOffsetsResult, error) {
	if strings.TrimSpace(req.Topic) == "" {
		return nil, errors.New("reset offsets: topic required")
	}
	switch req.Strategy {
	case ResetEarliest, ResetLatest, ResetToOffset, ResetTimestamp, ResetShiftBy:
	default:
		return nil, fmt.Errorf("reset offsets: unknown strategy %q", req.Strategy)
	}

	adm, err := r.Admin(cluster)
	if err != nil {
		return nil, err
	}

	// 1) Determine the target partitions — either user-supplied or "all partitions of topic".
	var targets []int32
	if len(req.Partitions) > 0 {
		seen := map[int32]struct{}{}
		for _, p := range req.Partitions {
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			targets = append(targets, p)
		}
	} else {
		td, err := adm.ListTopics(ctx, req.Topic)
		if err != nil {
			return nil, fmt.Errorf("list topic %q: %w", req.Topic, err)
		}
		t, ok := td[req.Topic]
		if !ok || t.Err != nil {
			if ok && t.Err != nil {
				return nil, fmt.Errorf("describe topic %q: %w", req.Topic, t.Err)
			}
			return nil, fmt.Errorf("topic %q not found", req.Topic)
		}
		for p := range t.Partitions {
			targets = append(targets, p)
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i] < targets[j] })

	// 2) Resolve new offsets per partition.
	var newOffsets kadm.ListedOffsets
	switch req.Strategy {
	case ResetEarliest:
		newOffsets, err = adm.ListStartOffsets(ctx, req.Topic)
	case ResetLatest:
		newOffsets, err = adm.ListEndOffsets(ctx, req.Topic)
	case ResetTimestamp:
		if req.TimestampMs <= 0 {
			return nil, errors.New("reset offsets: timestamp_ms required for timestamp strategy")
		}
		newOffsets, err = adm.ListOffsetsAfterMilli(ctx, req.TimestampMs, req.Topic)
	}
	if err != nil {
		return nil, fmt.Errorf("list offsets for topic %q: %w", req.Topic, err)
	}

	// 3) Fetch current committed offsets for the group (for OldOffset + shift-by).
	committed, cerr := adm.FetchOffsetsForTopics(ctx, group, req.Topic)
	if cerr != nil {
		return nil, fmt.Errorf("fetch committed offsets for group %q topic %q: %w", group, req.Topic, cerr)
	}

	// 4) Also fetch the log-end offsets so we can clamp user-provided absolute offsets
	//    to the valid range [start, end].
	ends, _ := adm.ListEndOffsets(ctx, req.Topic)
	starts, _ := adm.ListStartOffsets(ctx, req.Topic)

	toCommit := kadm.Offsets{}
	results := make([]ResetOffsetResult, 0, len(targets))
	for _, p := range targets {
		res := ResetOffsetResult{Partition: p, OldOffset: -1, NewOffset: -1}

		if c, ok := committed.Lookup(req.Topic, p); ok && c.Err == nil {
			res.OldOffset = c.At
		}

		var newAt int64 = -1
		switch req.Strategy {
		case ResetEarliest, ResetLatest, ResetTimestamp:
			if lo, ok := newOffsets.Lookup(req.Topic, p); ok {
				if lo.Err != nil {
					res.Error = lo.Err.Error()
				} else {
					newAt = lo.Offset
				}
			} else {
				res.Error = "partition offset not resolved"
			}
		case ResetToOffset:
			newAt = req.Offset
			if s, ok := starts.Lookup(req.Topic, p); ok && s.Err == nil && newAt < s.Offset {
				newAt = s.Offset
			}
			if e, ok := ends.Lookup(req.Topic, p); ok && e.Err == nil && newAt > e.Offset {
				newAt = e.Offset
			}
		case ResetShiftBy:
			if res.OldOffset < 0 {
				res.Error = "no prior commit to shift from"
			} else {
				newAt = res.OldOffset + req.Shift
				if s, ok := starts.Lookup(req.Topic, p); ok && s.Err == nil && newAt < s.Offset {
					newAt = s.Offset
				}
				if e, ok := ends.Lookup(req.Topic, p); ok && e.Err == nil && newAt > e.Offset {
					newAt = e.Offset
				}
			}
		}

		res.NewOffset = newAt
		if res.Error == "" && newAt >= 0 {
			toCommit.AddOffset(req.Topic, p, newAt, -1)
		}
		results = append(results, res)
	}

	out := &ResetOffsetsResult{
		Group:   group,
		Topic:   req.Topic,
		DryRun:  req.DryRun,
		Results: results,
	}

	if req.DryRun || len(toCommit) == 0 {
		return out, nil
	}

	commitResp, cerr := adm.CommitOffsets(ctx, group, toCommit)
	if cerr != nil {
		return nil, fmt.Errorf("commit offsets for group %q topic %q: %w", group, req.Topic, cerr)
	}
	// Merge per-partition commit errors back into the result.
	for i, r := range out.Results {
		if r.Error != "" || r.NewOffset < 0 {
			continue
		}
		if parts, ok := commitResp[req.Topic]; ok {
			if ore, ok := parts[r.Partition]; ok && ore.Err != nil {
				out.Results[i].Error = ore.Err.Error()
			}
		}
	}
	return out, nil
}

// DeleteGroup removes an empty/dead consumer group.
func (r *Registry) DeleteGroup(ctx context.Context, cluster, group string) error {
	adm, err := r.Admin(cluster)
	if err != nil {
		return err
	}
	resp, err := adm.DeleteGroup(ctx, group)
	if err != nil {
		return fmt.Errorf("delete group %q: %w", group, err)
	}
	if resp.Err != nil {
		return fmt.Errorf("delete group %q: %w", group, resp.Err)
	}
	return nil
}
